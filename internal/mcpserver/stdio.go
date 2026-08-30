// The stdio transport: the same MCP server over a pipe instead of a port.
//
// This is what `liteapi mcp` serves (internal/core/headless_mcp.go). The MCP
// stdio transport is newline-delimited JSON-RPC: one message per line on stdin,
// one response per line on stdout, and NOTHING ELSE ON STDOUT — a stray log
// line there is not a diagnostic, it is a protocol error at the client, which
// is why every message this file wants to emit goes to the logs writer
// (stderr in production) and why the loop never prints.
//
// NO BEARER TOKEN, DELIBERATELY. Over HTTP the token is what separates the
// user's collections from anything else that can reach the loopback interface.
// A pipe has no such ambiguity: stdin and stdout were handed to this process by
// the parent that launched it, so possession of the pipe IS the credential and
// a token would only be a second copy of a fact the operating system already
// guarantees. Adding one would also be unenforceable in the useful direction —
// a client that can write to stdin can write whatever token it likes. The
// consent boundary moves accordingly: invoking `liteapi mcp` is the consent,
// which is why the subcommand serves regardless of the Settings toggle that
// governs the HTTP listener.
package mcpserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// NewStdio builds a server for the stdio transport: same backend, same tools,
// same audit recorder, no token and no port.
//
// It is a separate constructor rather than New(backend, "", 0) so that the
// empty token is a decision this package states once, here, instead of a bare
// "" at a call site that reads like an oversight. A server built this way must
// never be Start()ed — authorized() denies every request when the token is
// empty, which is the correct answer for an HTTP endpoint with no credential
// and the reason the two constructors are not interchangeable.
func NewStdio(backend Backend, options ...Option) *Server {
	return New(backend, "", 0, options...)
}

// maxStdioLineBytes caps one inbound line, mirroring maxRequestBytes on the
// HTTP side. The pipe belongs to the parent process, so this is not a defence
// against a hostile peer; it is a bound on what a confused or runaway client
// can make this process allocate before being told it went wrong.
const maxStdioLineBytes = maxRequestBytes

// ServeStdio runs the stdio transport until the client closes stdin or ctx is
// cancelled, and returns nil for either — both are the ordinary end of a
// session, not failures. A write failure (the client went away mid-response) is
// returned, because the session ended in a state the caller should report.
//
// MESSAGES ARE HANDLED ONE AT A TIME. The HTTP transport serves concurrently
// because net/http does; here a run_request holds the loop for its duration
// (bounded by RunTimeout, which tools.go applies to the call itself). That is
// the honest shape for a pipe: responses stay in request order, stdout has a
// single writer and cannot interleave two half-written messages, and the client
// on the other end is one agent making one call at a time.
//
// CANCELLATION IS OBSERVED BETWEEN MESSAGES, and the two halves of that are
// different. A read already blocked on stdin cannot be interrupted portably, so
// the reader runs on its own goroutine and an idle session ends the moment the
// signal arrives, leaving that goroutine parked on a read the exiting process is
// about to close. A CALL already running is not cut short: the loop is inside it
// and reaches the next select only when it returns, so a run in flight keeps the
// RunTimeout tools.go gave it and the state it wrote lands before the app shuts
// down. Ctrl-C during a long run therefore waits for that run, deliberately — a
// half-written response and a half-recorded history entry are worse than a slow
// exit.
func (s *Server) ServeStdio(ctx context.Context, in io.Reader, out io.Writer, logs io.Writer) error {
	if logs == nil {
		logs = io.Discard
	}
	lines := make(chan stdioLine)
	go readStdioLines(ctx, in, lines)

	for {
		select {
		case <-ctx.Done():
			// SIGINT/SIGTERM. The caller shuts the app down and exits; saying so
			// on stderr keeps a killed session distinguishable from a client
			// that simply closed the pipe.
			// The diagnostic is best effort: stderr may itself be closed, and a
			// failure to explain the shutdown must not change what is returned.
			_, _ = fmt.Fprintln(logs, "liteapi mcp: interrupted, shutting down")
			return nil
		case line, open := <-lines:
			if !open {
				// EOF: the client closed stdin. Clean end of session.
				return nil
			}
			if line.err != nil {
				_, _ = fmt.Fprintf(logs, "liteapi mcp: reading stdin: %v\n", line.err)
				return line.err
			}
			response, ok := s.stdioResponse(line)
			if !ok {
				continue
			}
			if err := writeStdioMessage(out, response); err != nil {
				_, _ = fmt.Fprintf(logs, "liteapi mcp: writing to stdout: %v\n", err)
				return err
			}
		}
	}
}

// stdioResponse decides what one inbound line is answered with. ok is false
// when the line calls for no response at all: a blank line, which is framing
// noise rather than a message, or a notification, which by definition gets
// none.
func (s *Server) stdioResponse(line stdioLine) (rpcResponse, bool) {
	if line.oversize {
		// The line was discarded as it was read, so there is no id to echo and
		// no way to know whether one was even present. Null id, as JSON-RPC
		// requires when the request could not be read.
		return errorResponse(nullID, codeParseError,
			fmt.Sprintf("message exceeds the %d byte limit for a single JSON-RPC message", maxStdioLineBytes)), true
	}
	// A line with nothing on it is framing, not a message. HTTP has no
	// equivalent — a request always arrives, even an empty one, which is why an
	// empty body IS a parse error there — so this is the one place the two
	// transports legitimately differ.
	if len(bytes.TrimSpace(line.data)) == 0 {
		return rpcResponse{}, false
	}
	response, _ := s.handleMessage(line.data)
	if response == nil {
		return rpcResponse{}, false
	}
	return *response, true
}

// writeStdioMessage emits one response as a single line.
//
// One Write call for the message and its terminator together: stdout is shared
// with nothing else here, but a message split across two writes is a message
// another writer could interleave with, and the framing is only worth as much
// as that guarantee.
func writeStdioMessage(out io.Writer, response rpcResponse) error {
	encoded, err := json.Marshal(response)
	if err != nil {
		// Nothing built by this package contains a value encoding/json can
		// refuse, so this is a bug here rather than anything the client did.
		// It must still not be silent, and it must still not print to stdout.
		return fmt.Errorf("encode response: %w", err)
	}
	// json.Marshal never emits a raw newline (it escapes them inside strings),
	// so the terminator below is unambiguous.
	_, err = out.Write(append(encoded, '\n'))
	return err
}

// stdioLine is one line lifted off stdin: its bytes, whether it was too long to
// keep, and the read error that ended the stream.
type stdioLine struct {
	data     []byte
	oversize bool
	err      error
}

// readStdioLines feeds the loop until the stream ends, then closes the channel.
//
// A trailing line with no newline is delivered before the close: clients are
// meant to terminate every message with one, but a message already complete
// should not be dropped because the process on the other end exited between the
// two writes.
//
// Every send is guarded by ctx, because the receiver stops receiving the moment
// a signal arrives. In production the process is exiting either way; in a test
// the guard is what keeps a cancelled session from leaving a goroutine parked
// on a send forever.
func readStdioLines(ctx context.Context, in io.Reader, lines chan<- stdioLine) {
	defer close(lines)
	send := func(line stdioLine) bool {
		select {
		case lines <- line:
			return true
		case <-ctx.Done():
			return false
		}
	}
	reader := bufio.NewReader(in)
	for {
		data, oversize, err := readStdioLine(reader, maxStdioLineBytes)
		if len(data) > 0 || oversize {
			if !send(stdioLine{data: data, oversize: oversize}) {
				return
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				send(stdioLine{err: err})
			}
			return
		}
	}
}

// readStdioLine reads one newline-terminated line, capped at limit bytes.
//
// An over-long line is DRAINED rather than truncated: keeping its first limit
// bytes would hand handleMessage a fragment of JSON that parses as something
// else, and stopping the read would leave the tail of the line to be read as
// the next message. Draining costs nothing but time and leaves the stream
// framed correctly for whatever comes after, so a client that sends one absurd
// message can be told so and keep working.
func readStdioLine(reader *bufio.Reader, limit int) ([]byte, bool, error) {
	var line []byte
	oversize := false
	for {
		// ReadSlice returns a view into the reader's own buffer, valid only
		// until the next read — hence the copy that append performs.
		chunk, err := reader.ReadSlice('\n')
		switch {
		case oversize:
			// Draining: the line is already lost, only its end matters.
		case len(line)+len(chunk) > limit:
			oversize = true
			line = nil
		default:
			line = append(line, chunk...)
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil {
			return trimLineEnding(line), oversize, err
		}
		return trimLineEnding(line), oversize, nil
	}
}

// trimLineEnding drops the delimiter and a CR before it, so a client writing
// CRLF is read the same as one writing LF.
func trimLineEnding(line []byte) []byte {
	if n := len(line); n > 0 && line[n-1] == '\n' {
		line = line[:n-1]
	}
	if n := len(line); n > 0 && line[n-1] == '\r' {
		line = line[:n-1]
	}
	return line
}
