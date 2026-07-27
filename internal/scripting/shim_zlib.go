package scripting

// The sandbox node:zlib shim.
//
// Split out of scripting.go by AST: declarations are identified by the parser
// and copied verbatim from their source offsets.

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"encoding/base64"
	"fmt"
	"io"

	"github.com/andybalholm/brotli"
	"github.com/dop251/goja"
)

func newScriptZlibObject(runtime *goja.Runtime) goja.Value {
	_ = runtime.Set("__liteApiZlibCompress", func(kind, inputBase64 string, level int) (string, error) {
		return scriptZlibCompress(kind, inputBase64, level)
	})
	_ = runtime.Set("__liteApiZlibDecompress", func(kind, inputBase64 string) (string, error) {
		return scriptZlibDecompress(kind, inputBase64)
	})
	script := `(function () {
  const compressBridge = globalThis.__liteApiZlibCompress;
  const decompressBridge = globalThis.__liteApiZlibDecompress;

  const constants = {
    Z_NO_COMPRESSION: 0,
    Z_BEST_SPEED: 1,
    Z_BEST_COMPRESSION: 9,
    Z_DEFAULT_COMPRESSION: -1,
    Z_FILTERED: 1,
    Z_HUFFMAN_ONLY: 2,
    Z_RLE: 3,
    Z_FIXED: 4,
    Z_DEFAULT_STRATEGY: 0,
    Z_DEFLATED: 8,
    Z_OK: 0,
    Z_STREAM_END: 1,
    Z_NEED_DICT: 2,
    Z_ERRNO: -1,
    Z_STREAM_ERROR: -2,
    Z_DATA_ERROR: -3,
    Z_MEM_ERROR: -4,
    Z_BUF_ERROR: -5,
    Z_VERSION_ERROR: -6,
    BROTLI_OPERATION_PROCESS: 0,
    BROTLI_OPERATION_FLUSH: 1,
    BROTLI_OPERATION_FINISH: 2,
    BROTLI_PARAM_MODE: 0,
    BROTLI_PARAM_QUALITY: 1,
    BROTLI_PARAM_LGWIN: 2,
    BROTLI_MIN_QUALITY: 0,
    BROTLI_MAX_QUALITY: 11,
    BROTLI_DEFAULT_QUALITY: 6
  };

  function normalizeLevel(options, brotli) {
    let value;
    if (typeof options === "number") {
      value = options;
    } else if (options && typeof options === "object") {
      if (options.level !== undefined) {
        value = options.level;
      } else if (brotli && options.quality !== undefined) {
        value = options.quality;
      } else if (brotli && options.params && options.params[constants.BROTLI_PARAM_QUALITY] !== undefined) {
        value = options.params[constants.BROTLI_PARAM_QUALITY];
      }
    }
    const number = Number(value);
    return Number.isFinite(number) ? Math.trunc(number) : -1;
  }

  function inputBase64(input) {
    return Buffer.from(input).toString("base64");
  }

  function outputBuffer(base64) {
    return Buffer.from(String(base64 || ""), "base64");
  }

  function compressSync(kind, input, options, brotli) {
    return outputBuffer(compressBridge(kind, inputBase64(input), normalizeLevel(options, brotli)));
  }

  function decompressSync(kind, input) {
    return outputBuffer(decompressBridge(kind, inputBase64(input)));
  }

  function withCallback(operation) {
    return function (input, options, callback) {
      if (typeof options === "function") {
        callback = options;
        options = undefined;
      }
      try {
        const result = operation(input, options);
        if (typeof callback === "function") {
          callback(null, result);
          return undefined;
        }
        return result;
      } catch (err) {
        if (typeof callback === "function") {
          callback(err);
          return undefined;
        }
        throw err;
      }
    };
  }

  const gzipSync = function (input, options) { return compressSync("gzip", input, options, false); };
  const gunzipSync = function (input) { return decompressSync("gunzip", input); };
  const deflateSync = function (input, options) { return compressSync("deflate", input, options, false); };
  const inflateSync = function (input) { return decompressSync("inflate", input); };
  const deflateRawSync = function (input, options) { return compressSync("deflateRaw", input, options, false); };
  const inflateRawSync = function (input) { return decompressSync("inflateRaw", input); };
  const brotliCompressSync = function (input, options) { return compressSync("brotli", input, options, true); };
  const brotliDecompressSync = function (input) { return decompressSync("brotli", input); };
  const unzipSync = function (input) { return decompressSync("unzip", input); };

  const module = {
    constants,
    gzipSync,
    gunzipSync,
    deflateSync,
    inflateSync,
    deflateRawSync,
    inflateRawSync,
    brotliCompressSync,
    brotliDecompressSync,
    unzipSync,
    gzip: withCallback(gzipSync),
    gunzip: withCallback(gunzipSync),
    deflate: withCallback(deflateSync),
    inflate: withCallback(inflateSync),
    deflateRaw: withCallback(deflateRawSync),
    inflateRaw: withCallback(inflateRawSync),
    brotliCompress: withCallback(brotliCompressSync),
    brotliDecompress: withCallback(brotliDecompressSync),
    unzip: withCallback(unzipSync)
  };
  for (const key of Object.keys(constants)) {
    module[key] = constants[key];
  }
  return module;
})()`
	value, err := runtime.RunProgram(scriptZlibShim.compiled(script))
	if err != nil {
		panic(runtime.NewGoError(err))
	}
	_ = runtime.Set("__liteApiZlibCompress", goja.Undefined())
	_ = runtime.Set("__liteApiZlibDecompress", goja.Undefined())
	return value
}

func scriptZlibCompress(kind, inputBase64 string, level int) (string, error) {
	data, err := base64.StdEncoding.DecodeString(inputBase64)
	if err != nil {
		return "", err
	}
	level = scriptZlibFlateLevel(level)
	var out bytes.Buffer
	var writer io.WriteCloser
	switch kind {
	case "gzip":
		writer, err = gzip.NewWriterLevel(&out, level)
	case "deflate":
		writer, err = zlib.NewWriterLevel(&out, level)
	case "deflateRaw":
		writer, err = flate.NewWriter(&out, level)
	case "brotli":
		writer = brotli.NewWriterLevel(&out, scriptZlibBrotliLevel(level))
	default:
		return "", fmt.Errorf("unsupported zlib compression kind %q", kind)
	}
	if err != nil {
		return "", err
	}
	if _, err := writer.Write(data); err != nil {
		_ = writer.Close()
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(out.Bytes()), nil
}

func scriptZlibDecompress(kind, inputBase64 string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(inputBase64)
	if err != nil {
		return "", err
	}
	out, err := scriptZlibDecompressBytes(kind, data)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(out), nil
}

func scriptZlibDecompressBytes(kind string, data []byte) ([]byte, error) {
	reader := bytes.NewReader(data)
	var closer io.ReadCloser
	var stream io.Reader
	var err error
	switch kind {
	case "gunzip":
		closer, err = gzip.NewReader(reader)
		stream = closer
	case "inflate":
		closer, err = zlib.NewReader(reader)
		stream = closer
	case "inflateRaw":
		closer = flate.NewReader(reader)
		stream = closer
	case "brotli":
		stream = brotli.NewReader(reader)
	case "unzip":
		if out, unzipErr := scriptZlibDecompressBytes("gunzip", data); unzipErr == nil {
			return out, nil
		}
		return scriptZlibDecompressBytes("inflate", data)
	default:
		return nil, fmt.Errorf("unsupported zlib decompression kind %q", kind)
	}
	if err != nil {
		return nil, err
	}
	out, err := io.ReadAll(stream)
	if closer != nil {
		if closeErr := closer.Close(); err == nil {
			err = closeErr
		}
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

func scriptZlibFlateLevel(level int) int {
	if level == flate.HuffmanOnly || level == flate.DefaultCompression || (level >= flate.NoCompression && level <= flate.BestCompression) {
		return level
	}
	return flate.DefaultCompression
}

func scriptZlibBrotliLevel(level int) int {
	if level == flate.DefaultCompression {
		return brotli.DefaultCompression
	}
	if level < brotli.BestSpeed {
		return brotli.BestSpeed
	}
	if level > brotli.BestCompression {
		return brotli.BestCompression
	}
	return level
}
