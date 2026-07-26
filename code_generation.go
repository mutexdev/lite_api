package main

// US-054 — code generation targets beyond curl and fetch.
//
// Nine languages, all fed from one normalised view of the request. The
// normalisation matters more than any individual emitter: without it, each
// generator re-derives the content type, re-decides whether a body exists and
// re-resolves the URL, and they drift — someone copies the Python and the Go
// and gets two different requests from the same row.
//
// ESCAPING IS THE RISK. A generator that mishandles a quote does not produce
// broken-looking output; it produces code that compiles and sends the wrong
// thing, or worse, code where a value breaks out of its literal. Every emitter
// therefore uses an explicit per-language quoting function, and the tests feed
// a body containing quotes, backslashes, newlines and a dollar sign through
// every target.

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
)

// codegenRequest is the single normalised view every emitter reads.
type codegenRequest struct {
	Method  string
	URL     string
	Headers []KeyValue
	// BodyKind is one of "none", "raw", "form", "multipart", "file".
	BodyKind    string
	RawBody     string
	ContentType string
	FormFields  []KeyValue
	Multipart   []FormPart
	FilePath    string
}

func newCodegenRequest(example ResponseExample) codegenRequest {
	req := example.Request
	out := codegenRequest{
		Method: strings.ToUpper(strings.TrimSpace(firstNonEmpty(req.Method, http.MethodGet))),
		URL:    requestURLWithParams(req.URL, req.Params, nil, nil),
	}
	out.Headers = enabledKeyValues(req.Headers)

	mode := normalizedBodyMode(req.BodyMode)
	out.ContentType = responseExampleRequestContentType(req)

	switch mode {
	case "formUrlEncoded":
		for _, field := range req.FormURLEncoded {
			if field.Enabled && strings.TrimSpace(field.Name) != "" {
				out.FormFields = append(out.FormFields, field)
			}
		}
		if len(out.FormFields) > 0 {
			out.BodyKind = "form"
		} else {
			out.BodyKind = "none"
		}
	case "multipartForm":
		for _, part := range req.MultipartForm {
			if part.Enabled && strings.TrimSpace(part.Name) != "" {
				out.Multipart = append(out.Multipart, part)
			}
		}
		if len(out.Multipart) > 0 {
			out.BodyKind = "multipart"
		} else {
			out.BodyKind = "none"
		}
	case "file":
		if len(req.File) > 0 {
			out.BodyKind = "file"
			out.FilePath = req.File[0].FilePath
		} else {
			out.BodyKind = "none"
		}
	default:
		// responseExampleRawBody, not req.Body: it is what suppresses the body
		// for mode "none". The app keeps the text when the user switches body
		// mode away so nothing is lost, so req.Body is routinely non-empty for
		// a request that sends nothing — reading it directly generates a
		// snippet that posts a payload the app itself would not send.
		if raw := responseExampleRawBody(req); strings.TrimSpace(raw) != "" {
			out.BodyKind = "raw"
			out.RawBody = raw
		} else {
			out.BodyKind = "none"
		}
	}

	// The content type header is added only when the body implies one the user
	// has not already set. Multipart is excluded because its header carries a
	// boundary the client generates — writing one by hand produces a request
	// the server cannot parse.
	if out.ContentType != "" && !hasHeaderName(out.Headers, "content-type") && out.BodyKind != "multipart" && out.BodyKind != "none" {
		out.Headers = append(out.Headers, KeyValue{Name: "Content-Type", Value: out.ContentType, Enabled: true})
	}
	return out
}

// formBodyString renders form fields as an encoded body for the languages that
// take one pre-encoded.
func (r codegenRequest) formBodyString() string {
	pairs := make([]string, 0, len(r.FormFields))
	for _, field := range r.FormFields {
		pairs = append(pairs, urlQueryEscape(field.Name)+"="+urlQueryEscape(field.Value))
	}
	return strings.Join(pairs, "&")
}

func urlQueryEscape(value string) string {
	// Deliberately not url.QueryEscape: that encodes a space as '+', which is
	// correct for form bodies but reads wrong in generated code and differs
	// from what every one of these libraries does natively.
	var builder strings.Builder
	for _, b := range []byte(value) {
		switch {
		case (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9'),
			b == '-', b == '_', b == '.', b == '~':
			builder.WriteByte(b)
		default:
			fmt.Fprintf(&builder, "%%%02X", b)
		}
	}
	return builder.String()
}

// --- quoting -------------------------------------------------------------
//
// One function per language family. They differ in more than the quote
// character: Python needs \n escaped inside a single-quoted string, PowerShell
// escapes a single quote by doubling it, PHP's double-quoted strings
// interpolate $variables while single-quoted ones do not, and Ruby's
// single-quoted strings escape only \ and '.

func pythonString(value string) string {
	return strconv.Quote(value) // Python and Go share double-quoted escapes.
}

func jsonishString(value string) string {
	return strconv.Quote(value)
}

func goStringLiteral(value string) string {
	return strconv.Quote(value)
}

func javaString(value string) string {
	return strconv.Quote(value)
}

func csharpString(value string) string {
	// C# verbatim strings would need "" doubling; a regular string with Go's
	// escaping is compatible for everything these bodies contain.
	return strconv.Quote(value)
}

// phpString uses SINGLE quotes so a $ in the body is not interpolated. A body
// containing "$total" emitted into a double-quoted PHP string silently becomes
// an empty variable reference.
func phpString(value string) string {
	escaped := strings.ReplaceAll(value, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `'`, `\'`)
	return "'" + escaped + "'"
}

// rubyString uses single quotes for the same reason: Ruby interpolates #{} in
// double-quoted strings, and a body containing #{ would be evaluated.
func rubyString(value string) string {
	escaped := strings.ReplaceAll(value, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `'`, `\'`)
	return "'" + escaped + "'"
}

// The POSIX single-quote escape used by the HTTPie emitter is shellSingleQuote
// in app.go, shared with the curl generator so the two cannot diverge.

// powershellString doubles single quotes, which is PowerShell's only escape
// inside a literal string. A backslash is NOT an escape there, so leaving it
// alone is correct — escaping it would corrupt every Windows path.
func powershellString(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

// --- emitters ------------------------------------------------------------

func generatePythonRequests(r codegenRequest) string {
	var b strings.Builder
	b.WriteString("import requests\n\n")
	b.WriteString("url = " + pythonString(r.URL) + "\n")

	if len(r.Headers) > 0 {
		b.WriteString("headers = {\n")
		for _, header := range r.Headers {
			b.WriteString("    " + pythonString(header.Name) + ": " + pythonString(header.Value) + ",\n")
		}
		b.WriteString("}\n")
	} else {
		b.WriteString("headers = {}\n")
	}

	call := "requests.request(" + pythonString(r.Method) + ", url, headers=headers"
	switch r.BodyKind {
	case "raw":
		b.WriteString("payload = " + pythonString(r.RawBody) + "\n")
		call += ", data=payload.encode(\"utf-8\")"
	case "form":
		b.WriteString("payload = {\n")
		for _, field := range r.FormFields {
			b.WriteString("    " + pythonString(field.Name) + ": " + pythonString(field.Value) + ",\n")
		}
		b.WriteString("}\n")
		call += ", data=payload"
	case "multipart":
		b.WriteString("files = {\n")
		for _, part := range r.Multipart {
			if strings.TrimSpace(part.FilePath) != "" {
				b.WriteString("    " + pythonString(part.Name) + ": open(" + pythonString(part.FilePath) + ", \"rb\"),\n")
			} else {
				b.WriteString("    " + pythonString(part.Name) + ": (None, " + pythonString(part.Value) + "),\n")
			}
		}
		b.WriteString("}\n")
		call += ", files=files"
	case "file":
		b.WriteString("with open(" + pythonString(r.FilePath) + ", \"rb\") as handle:\n")
		b.WriteString("    payload = handle.read()\n")
		call += ", data=payload"
	}
	call += ")"

	b.WriteString("\nresponse = " + call + "\n")
	b.WriteString("print(response.status_code)\nprint(response.text)\n")
	return b.String()
}

func generateNodeAxios(r codegenRequest) string {
	var b strings.Builder
	b.WriteString("const axios = require('axios');\n\n")
	b.WriteString("const config = {\n")
	b.WriteString("  method: " + jsonishString(strings.ToLower(r.Method)) + ",\n")
	b.WriteString("  url: " + jsonishString(r.URL) + ",\n")

	if len(r.Headers) > 0 {
		b.WriteString("  headers: {\n")
		for _, header := range r.Headers {
			b.WriteString("    " + jsonishString(header.Name) + ": " + jsonishString(header.Value) + ",\n")
		}
		b.WriteString("  },\n")
	}

	switch r.BodyKind {
	case "raw":
		b.WriteString("  data: " + jsonishString(r.RawBody) + ",\n")
	case "form":
		b.WriteString("  data: new URLSearchParams({\n")
		for _, field := range r.FormFields {
			b.WriteString("    " + jsonishString(field.Name) + ": " + jsonishString(field.Value) + ",\n")
		}
		b.WriteString("  }),\n")
	case "multipart":
		b.WriteString("  data: form,\n")
	case "file":
		b.WriteString("  data: require('fs').createReadStream(" + jsonishString(r.FilePath) + "),\n")
	}
	b.WriteString("};\n\n")

	if r.BodyKind == "multipart" {
		var form strings.Builder
		form.WriteString("const FormData = require('form-data');\nconst form = new FormData();\n")
		for _, part := range r.Multipart {
			if strings.TrimSpace(part.FilePath) != "" {
				form.WriteString("form.append(" + jsonishString(part.Name) + ", require('fs').createReadStream(" + jsonishString(part.FilePath) + "));\n")
			} else {
				form.WriteString("form.append(" + jsonishString(part.Name) + ", " + jsonishString(part.Value) + ");\n")
			}
		}
		form.WriteString("\n")
		return "const axios = require('axios');\n" + form.String() + strings.TrimPrefix(b.String(), "const axios = require('axios');\n\n") +
			"const response = await axios(config);\nconsole.log(response.status);\nconsole.log(response.data);\n"
	}

	b.WriteString("const response = await axios(config);\nconsole.log(response.status);\nconsole.log(response.data);\n")
	return b.String()
}

func generateGoNetHTTP(r codegenRequest) string {
	var b strings.Builder
	imports := []string{"fmt", "io", "net/http"}

	var bodyExpr string
	var setup strings.Builder
	switch r.BodyKind {
	case "raw":
		imports = append(imports, "strings")
		setup.WriteString("\tbody := strings.NewReader(" + goStringLiteral(r.RawBody) + ")\n")
		bodyExpr = "body"
	case "form":
		imports = append(imports, "net/url", "strings")
		setup.WriteString("\tform := url.Values{}\n")
		for _, field := range r.FormFields {
			setup.WriteString("\tform.Set(" + goStringLiteral(field.Name) + ", " + goStringLiteral(field.Value) + ")\n")
		}
		setup.WriteString("\tbody := strings.NewReader(form.Encode())\n")
		bodyExpr = "body"
	case "file":
		imports = append(imports, "os")
		setup.WriteString("\tbody, err := os.Open(" + goStringLiteral(r.FilePath) + ")\n")
		setup.WriteString("\tif err != nil {\n\t\tpanic(err)\n\t}\n\tdefer body.Close()\n")
		bodyExpr = "body"
	case "multipart":
		imports = append(imports, "bytes", "mime/multipart", "os", "path/filepath")
		setup.WriteString("\tvar buffer bytes.Buffer\n\twriter := multipart.NewWriter(&buffer)\n")
		for _, part := range r.Multipart {
			if strings.TrimSpace(part.FilePath) != "" {
				setup.WriteString("\tif file, err := os.Open(" + goStringLiteral(part.FilePath) + "); err == nil {\n")
				setup.WriteString("\t\tpart, _ := writer.CreateFormFile(" + goStringLiteral(part.Name) + ", filepath.Base(" + goStringLiteral(part.FilePath) + "))\n")
				setup.WriteString("\t\tio.Copy(part, file)\n\t\tfile.Close()\n\t}\n")
			} else {
				setup.WriteString("\twriter.WriteField(" + goStringLiteral(part.Name) + ", " + goStringLiteral(part.Value) + ")\n")
			}
		}
		setup.WriteString("\twriter.Close()\n\tbody := &buffer\n")
		bodyExpr = "body"
	default:
		bodyExpr = "nil"
	}

	b.WriteString("package main\n\nimport (\n")
	for _, name := range dedupeStrings(imports) {
		b.WriteString("\t\"" + name + "\"\n")
	}
	b.WriteString(")\n\nfunc main() {\n")
	b.WriteString(setup.String())
	b.WriteString("\treq, err := http.NewRequest(" + goStringLiteral(r.Method) + ", " + goStringLiteral(r.URL) + ", " + bodyExpr + ")\n")
	b.WriteString("\tif err != nil {\n\t\tpanic(err)\n\t}\n")
	for _, header := range r.Headers {
		b.WriteString("\treq.Header.Set(" + goStringLiteral(header.Name) + ", " + goStringLiteral(header.Value) + ")\n")
	}
	if r.BodyKind == "multipart" {
		b.WriteString("\treq.Header.Set(\"Content-Type\", writer.FormDataContentType())\n")
	}
	b.WriteString("\n\tresp, err := http.DefaultClient.Do(req)\n\tif err != nil {\n\t\tpanic(err)\n\t}\n\tdefer resp.Body.Close()\n\n")
	b.WriteString("\tdata, _ := io.ReadAll(resp.Body)\n\tfmt.Println(resp.StatusCode)\n\tfmt.Println(string(data))\n}\n")
	return b.String()
}

func dedupeStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func generateJavaHTTPClient(r codegenRequest) string {
	var b strings.Builder
	b.WriteString("import java.net.URI;\n")
	b.WriteString("import java.net.http.HttpClient;\n")
	b.WriteString("import java.net.http.HttpRequest;\n")
	b.WriteString("import java.net.http.HttpResponse;\n\n")
	b.WriteString("HttpClient client = HttpClient.newHttpClient();\n\n")

	publisher := "HttpRequest.BodyPublishers.noBody()"
	switch r.BodyKind {
	case "raw":
		b.WriteString("String body = " + javaString(r.RawBody) + ";\n")
		publisher = "HttpRequest.BodyPublishers.ofString(body)"
	case "form":
		b.WriteString("String body = " + javaString(r.formBodyString()) + ";\n")
		publisher = "HttpRequest.BodyPublishers.ofString(body)"
	case "file":
		b.WriteString("java.nio.file.Path body = java.nio.file.Path.of(" + javaString(r.FilePath) + ");\n")
		publisher = "HttpRequest.BodyPublishers.ofFile(body)"
	case "multipart":
		// java.net.http has no multipart publisher. Saying so beats emitting
		// code that looks complete and sends the fields as a raw string.
		b.WriteString("// java.net.http has no built-in multipart publisher; build the\n")
		b.WriteString("// body with an HTTP client that does, or assemble it by hand:\n")
		for _, part := range r.Multipart {
			b.WriteString("//   " + part.Name + " = " + firstNonEmpty(part.FilePath, part.Value) + "\n")
		}
		b.WriteString("\n")
	}

	b.WriteString("HttpRequest request = HttpRequest.newBuilder()\n")
	b.WriteString("    .uri(URI.create(" + javaString(r.URL) + "))\n")
	for _, header := range r.Headers {
		b.WriteString("    .header(" + javaString(header.Name) + ", " + javaString(header.Value) + ")\n")
	}
	b.WriteString("    .method(" + javaString(r.Method) + ", " + publisher + ")\n")
	b.WriteString("    .build();\n\n")
	b.WriteString("HttpResponse<String> response = client.send(request, HttpResponse.BodyHandlers.ofString());\n")
	b.WriteString("System.out.println(response.statusCode());\nSystem.out.println(response.body());\n")
	return b.String()
}

func generateCSharpHTTPClient(r codegenRequest) string {
	var b strings.Builder
	b.WriteString("using System;\nusing System.Net.Http;\nusing System.Text;\nusing System.Threading.Tasks;\n\n")
	b.WriteString("using var client = new HttpClient();\n")
	b.WriteString("using var request = new HttpRequestMessage(new HttpMethod(" + csharpString(r.Method) + "), " + csharpString(r.URL) + ");\n")

	contentHeaders := map[string]bool{"content-type": true, "content-length": true, "content-disposition": true}
	for _, header := range r.Headers {
		if contentHeaders[strings.ToLower(header.Name)] {
			// Content headers belong on the content, not the request, and
			// HttpClient throws rather than ignoring a misplaced one.
			continue
		}
		b.WriteString("request.Headers.TryAddWithoutValidation(" + csharpString(header.Name) + ", " + csharpString(header.Value) + ");\n")
	}

	switch r.BodyKind {
	case "raw":
		b.WriteString("request.Content = new StringContent(" + csharpString(r.RawBody) + ", Encoding.UTF8, " + csharpString(firstNonEmpty(r.ContentType, "text/plain")) + ");\n")
	case "form":
		b.WriteString("request.Content = new FormUrlEncodedContent(new[]\n{\n")
		for _, field := range r.FormFields {
			b.WriteString("    new KeyValuePair<string, string>(" + csharpString(field.Name) + ", " + csharpString(field.Value) + "),\n")
		}
		b.WriteString("});\n")
	case "multipart":
		b.WriteString("var form = new MultipartFormDataContent();\n")
		for _, part := range r.Multipart {
			if strings.TrimSpace(part.FilePath) != "" {
				b.WriteString("form.Add(new StreamContent(System.IO.File.OpenRead(" + csharpString(part.FilePath) + ")), " + csharpString(part.Name) + ", " + csharpString(filepath.Base(part.FilePath)) + ");\n")
			} else {
				b.WriteString("form.Add(new StringContent(" + csharpString(part.Value) + "), " + csharpString(part.Name) + ");\n")
			}
		}
		b.WriteString("request.Content = form;\n")
	case "file":
		b.WriteString("request.Content = new StreamContent(System.IO.File.OpenRead(" + csharpString(r.FilePath) + "));\n")
	}

	b.WriteString("\nvar response = await client.SendAsync(request);\n")
	b.WriteString("Console.WriteLine((int)response.StatusCode);\n")
	b.WriteString("Console.WriteLine(await response.Content.ReadAsStringAsync());\n")
	return b.String()
}

func generatePHPCurl(r codegenRequest) string {
	var b strings.Builder
	b.WriteString("<?php\n\n$ch = curl_init();\n\n")
	b.WriteString("curl_setopt($ch, CURLOPT_URL, " + phpString(r.URL) + ");\n")
	b.WriteString("curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);\n")
	b.WriteString("curl_setopt($ch, CURLOPT_CUSTOMREQUEST, " + phpString(r.Method) + ");\n")

	if len(r.Headers) > 0 {
		b.WriteString("curl_setopt($ch, CURLOPT_HTTPHEADER, [\n")
		for _, header := range r.Headers {
			b.WriteString("    " + phpString(header.Name+": "+header.Value) + ",\n")
		}
		b.WriteString("]);\n")
	}

	switch r.BodyKind {
	case "raw":
		b.WriteString("curl_setopt($ch, CURLOPT_POSTFIELDS, " + phpString(r.RawBody) + ");\n")
	case "form":
		b.WriteString("curl_setopt($ch, CURLOPT_POSTFIELDS, " + phpString(r.formBodyString()) + ");\n")
	case "multipart":
		b.WriteString("curl_setopt($ch, CURLOPT_POSTFIELDS, [\n")
		for _, part := range r.Multipart {
			if strings.TrimSpace(part.FilePath) != "" {
				b.WriteString("    " + phpString(part.Name) + " => new CURLFile(" + phpString(part.FilePath) + "),\n")
			} else {
				b.WriteString("    " + phpString(part.Name) + " => " + phpString(part.Value) + ",\n")
			}
		}
		b.WriteString("]);\n")
	case "file":
		b.WriteString("curl_setopt($ch, CURLOPT_POSTFIELDS, file_get_contents(" + phpString(r.FilePath) + "));\n")
	}

	b.WriteString("\n$response = curl_exec($ch);\n")
	b.WriteString("echo curl_getinfo($ch, CURLINFO_HTTP_CODE), PHP_EOL;\n")
	b.WriteString("echo $response, PHP_EOL;\n")
	b.WriteString("curl_close($ch);\n")
	return b.String()
}

func generateRubyNetHTTP(r codegenRequest) string {
	var b strings.Builder
	b.WriteString("require 'net/http'\nrequire 'uri'\n\n")
	b.WriteString("uri = URI(" + rubyString(r.URL) + ")\n")
	// Net::HTTP::Get and friends are separate classes; GenericRequest takes the
	// method as a string and so handles PATCH, custom verbs and everything else
	// uniformly.
	b.WriteString("request = Net::HTTPGenericRequest.new(" + rubyString(r.Method) + ", true, true, uri)\n")
	for _, header := range r.Headers {
		b.WriteString("request[" + rubyString(header.Name) + "] = " + rubyString(header.Value) + "\n")
	}

	switch r.BodyKind {
	case "raw":
		b.WriteString("request.body = " + rubyString(r.RawBody) + "\n")
	case "form":
		b.WriteString("request.body = " + rubyString(r.formBodyString()) + "\n")
	case "file":
		b.WriteString("request.body = File.binread(" + rubyString(r.FilePath) + ")\n")
	case "multipart":
		b.WriteString("request.set_form([\n")
		for _, part := range r.Multipart {
			if strings.TrimSpace(part.FilePath) != "" {
				b.WriteString("  [" + rubyString(part.Name) + ", File.open(" + rubyString(part.FilePath) + ")],\n")
			} else {
				b.WriteString("  [" + rubyString(part.Name) + ", " + rubyString(part.Value) + "],\n")
			}
		}
		b.WriteString("], 'multipart/form-data')\n")
	}

	b.WriteString("\nresponse = Net::HTTP.start(uri.hostname, uri.port, use_ssl: uri.scheme == 'https') do |http|\n")
	b.WriteString("  http.request(request)\nend\n\n")
	b.WriteString("puts response.code\nputs response.body\n")
	return b.String()
}

func generateHTTPie(r codegenRequest) string {
	parts := []string{"http", "--print=hb", r.Method, shellSingleQuote(r.URL)}
	for _, header := range r.Headers {
		parts = append(parts, shellSingleQuote(header.Name+":"+header.Value))
	}

	switch r.BodyKind {
	case "form":
		parts = append(parts, "--form")
		for _, field := range r.FormFields {
			parts = append(parts, shellSingleQuote(field.Name+"="+field.Value))
		}
	case "multipart":
		parts = append(parts, "--form")
		for _, part := range r.Multipart {
			if strings.TrimSpace(part.FilePath) != "" {
				// @ tells HTTPie to upload the file rather than send its path
				// as a string value.
				parts = append(parts, shellSingleQuote(part.Name+"@"+part.FilePath))
			} else {
				parts = append(parts, shellSingleQuote(part.Name+"="+part.Value))
			}
		}
	case "file":
		return strings.Join(parts, " \\\n  ") + " \\\n  < " + shellSingleQuote(r.FilePath) + "\n"
	case "raw":
		// Piped in rather than passed as an argument: a large JSON body as an
		// argument hits the shell's argument length limit, and quoting a body
		// containing single quotes on the command line is where this breaks.
		return "echo " + shellSingleQuote(r.RawBody) + " | \\\n  " + strings.Join(parts, " \\\n  ") + "\n"
	}
	return strings.Join(parts, " \\\n  ") + "\n"
}

func generatePowerShell(r codegenRequest) string {
	var b strings.Builder
	b.WriteString("$headers = @{}\n")
	for _, header := range r.Headers {
		// Content-Type is passed with -ContentType, not in the header table;
		// Invoke-RestMethod errors on the duplicate.
		if strings.EqualFold(header.Name, "content-type") {
			continue
		}
		b.WriteString("$headers[" + powershellString(header.Name) + "] = " + powershellString(header.Value) + "\n")
	}
	b.WriteString("\n")

	command := "Invoke-RestMethod -Method " + r.Method + " -Uri " + powershellString(r.URL) + " -Headers $headers"

	switch r.BodyKind {
	case "raw":
		b.WriteString("$body = " + powershellString(r.RawBody) + "\n\n")
		command += " -Body $body"
	case "form":
		b.WriteString("$body = @{\n")
		for _, field := range r.FormFields {
			b.WriteString("  " + powershellString(field.Name) + " = " + powershellString(field.Value) + "\n")
		}
		b.WriteString("}\n\n")
		command += " -Body $body"
	case "multipart":
		b.WriteString("$form = @{\n")
		for _, part := range r.Multipart {
			if strings.TrimSpace(part.FilePath) != "" {
				b.WriteString("  " + powershellString(part.Name) + " = Get-Item " + powershellString(part.FilePath) + "\n")
			} else {
				b.WriteString("  " + powershellString(part.Name) + " = " + powershellString(part.Value) + "\n")
			}
		}
		b.WriteString("}\n\n")
		command += " -Form $form"
	case "file":
		command += " -InFile " + powershellString(r.FilePath)
	}

	if r.ContentType != "" && r.BodyKind != "multipart" && r.BodyKind != "none" {
		command += " -ContentType " + powershellString(r.ContentType)
	}

	b.WriteString("$response = " + command + "\n$response | ConvertTo-Json -Depth 10\n")
	return b.String()
}

// codegenLanguages lists every target and the aliases it answers to.
//
// Ordered, because this list drives the UI picker and a map would reorder it on
// every render.
var codegenLanguages = []struct {
	ID      string
	Label   string
	Aliases []string
	Emit    func(codegenRequest) string
}{
	{ID: "python", Label: "Python (requests)", Aliases: []string{"py", "python-requests", "requests"}, Emit: generatePythonRequests},
	{ID: "axios", Label: "Node (axios)", Aliases: []string{"node-axios", "javascript-axios"}, Emit: generateNodeAxios},
	{ID: "go", Label: "Go (net/http)", Aliases: []string{"golang", "go-http"}, Emit: generateGoNetHTTP},
	{ID: "java", Label: "Java (java.net.http)", Aliases: []string{"java-http"}, Emit: generateJavaHTTPClient},
	{ID: "csharp", Label: "C# (HttpClient)", Aliases: []string{"c#", "cs", "dotnet"}, Emit: generateCSharpHTTPClient},
	{ID: "php", Label: "PHP (cURL)", Aliases: []string{"php-curl"}, Emit: generatePHPCurl},
	{ID: "ruby", Label: "Ruby (net/http)", Aliases: []string{"rb", "ruby-http"}, Emit: generateRubyNetHTTP},
	{ID: "httpie", Label: "HTTPie", Aliases: []string{"http"}, Emit: generateHTTPie},
	{ID: "powershell", Label: "PowerShell", Aliases: []string{"pwsh", "ps1"}, Emit: generatePowerShell},
}

// generateExtendedCode returns the code for one of the US-054 targets, and
// reports whether the language was one of them.
func generateExtendedCode(example ResponseExample, language string) (string, bool) {
	wanted := strings.ToLower(strings.TrimSpace(language))
	if wanted == "" {
		return "", false
	}
	for _, target := range codegenLanguages {
		if target.ID == wanted {
			return target.Emit(newCodegenRequest(example)), true
		}
		for _, alias := range target.Aliases {
			if alias == wanted {
				return target.Emit(newCodegenRequest(example)), true
			}
		}
	}
	return "", false
}

// CodeGenerationTarget is one entry in the language picker.
type CodeGenerationTarget struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// CodeGenerationTargets lists every language the generator supports, so the UI
// picker is built from the same list the generator dispatches on and the two
// cannot disagree.
func (a *App) CodeGenerationTargets() []CodeGenerationTarget {
	out := []CodeGenerationTarget{
		{ID: "curl", Label: "cURL"},
		{ID: "fetch", Label: "JavaScript (fetch)"},
	}
	for _, target := range codegenLanguages {
		out = append(out, CodeGenerationTarget{ID: target.ID, Label: target.Label})
	}
	return out
}
