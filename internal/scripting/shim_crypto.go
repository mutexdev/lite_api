package scripting

// The sandbox WebCrypto and crypto-js shims.
//
// Split out of scripting.go by AST: declarations are identified by the parser
// and copied verbatim from their source offsets.

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"strconv"
	"strings"

	"github.com/dop251/goja"
	"golang.org/x/crypto/pbkdf2"
	"golang.org/x/crypto/scrypt"
)

func newScriptCryptoObject(runtime *goja.Runtime) *goja.Object {
	cryptoObject := runtime.NewObject()
	_ = cryptoObject.Set("createHash", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			panic(runtime.NewTypeError("algorithm is required"))
		}
		hasher, err := newScriptCryptoHash(call.Argument(0).String())
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		return newScriptCryptoDigestObject(runtime, hasher)
	})
	_ = cryptoObject.Set("createHmac", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			panic(runtime.NewTypeError("algorithm and key are required"))
		}
		factory, err := scriptCryptoHashFactory(call.Argument(0).String())
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		key, err := scriptCryptoValueBytes(runtime, call.Argument(1), "")
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		return newScriptCryptoDigestObject(runtime, hmac.New(factory, key))
	})
	_ = cryptoObject.Set("getHashes", func() []string {
		return []string{"md5", "sha1", "sha224", "sha256", "sha384", "sha512"}
	})
	_ = cryptoObject.Set("getCiphers", func() []string {
		return []string{"aes-128-cbc", "aes-192-cbc", "aes-256-cbc"}
	})
	_ = cryptoObject.Set("pbkdf2Sync", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 5 {
			panic(runtime.NewTypeError("password, salt, iterations, key length, and digest are required"))
		}
		password, err := scriptCryptoValueBytes(runtime, call.Argument(0), "")
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		salt, err := scriptCryptoValueBytes(runtime, call.Argument(1), "")
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		iterations := int(call.Argument(2).ToInteger())
		keyLength := int(call.Argument(3).ToInteger())
		if iterations <= 0 || keyLength < 0 {
			panic(runtime.NewTypeError("iterations must be positive and key length must be non-negative"))
		}
		factory, err := scriptCryptoHashFactory(call.Argument(4).String())
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		return scriptBufferValue(runtime, pbkdf2.Key(password, salt, iterations, keyLength, factory))
	})
	_ = cryptoObject.Set("scryptSync", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 3 {
			panic(runtime.NewTypeError("password, salt, and key length are required"))
		}
		password, err := scriptCryptoValueBytes(runtime, call.Argument(0), "")
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		salt, err := scriptCryptoValueBytes(runtime, call.Argument(1), "")
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		keyLength := int(call.Argument(2).ToInteger())
		if keyLength < 0 {
			panic(runtime.NewTypeError("key length must be non-negative"))
		}
		key, err := scrypt.Key(password, salt, 1<<14, 8, 1, keyLength)
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		return scriptBufferValue(runtime, key)
	})
	_ = cryptoObject.Set("timingSafeEqual", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			panic(runtime.NewTypeError("two buffers are required"))
		}
		left, err := scriptCryptoValueBytes(runtime, call.Argument(0), "")
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		right, err := scriptCryptoValueBytes(runtime, call.Argument(1), "")
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		if len(left) != len(right) {
			panic(runtime.NewTypeError("Input buffers must have the same byte length"))
		}
		return runtime.ToValue(hmac.Equal(left, right))
	})
	_ = cryptoObject.Set("createCipheriv", func(call goja.FunctionCall) goja.Value {
		return newScriptAESCBCObject(runtime, call, true)
	})
	_ = cryptoObject.Set("createDecipheriv", func(call goja.FunctionCall) goja.Value {
		return newScriptAESCBCObject(runtime, call, false)
	})
	_ = cryptoObject.Set("randomBytes", func(call goja.FunctionCall) goja.Value {
		size := 0
		if len(call.Arguments) > 0 {
			size = int(call.Argument(0).ToInteger())
		}
		if size < 0 {
			panic(runtime.NewTypeError("size must be non-negative"))
		}
		bytes := make([]byte, size)
		if _, err := rand.Read(bytes); err != nil {
			panic(runtime.NewGoError(err))
		}
		return scriptBufferValue(runtime, bytes)
	})
	_ = cryptoObject.Set("randomUUID", func() string { return scriptRandomUUID() })
	_ = cryptoObject.Set("getRandomValues", func(call goja.FunctionCall) goja.Value {
		target := call.Argument(0)
		if target == nil || goja.IsUndefined(target) || goja.IsNull(target) {
			panic(runtime.NewTypeError("expected typed array"))
		}
		targetObject := target.ToObject(runtime)
		length := int(targetObject.Get("length").ToInteger())
		if length < 0 {
			panic(runtime.NewTypeError("length must be non-negative"))
		}
		bytes := make([]byte, length)
		if _, err := rand.Read(bytes); err != nil {
			panic(runtime.NewGoError(err))
		}
		for index, value := range bytes {
			_ = targetObject.Set(strconv.Itoa(index), int(value))
		}
		return target
	})
	_ = cryptoObject.Set("subtle", newScriptSubtleCryptoObject(runtime))
	return cryptoObject
}

func newScriptSubtleCryptoObject(runtime *goja.Runtime) *goja.Object {
	subtle := runtime.NewObject()
	_ = subtle.Set("digest", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			return scriptRejectedPromise(runtime, runtime.NewTypeError("algorithm and data are required"))
		}
		hasher, err := newScriptCryptoHash(scriptWebCryptoAlgorithmName(runtime, call.Argument(0)))
		if err != nil {
			return scriptRejectedPromise(runtime, runtime.NewGoError(err))
		}
		data, err := scriptCryptoValueBytes(runtime, call.Argument(1), "")
		if err != nil {
			return scriptRejectedPromise(runtime, runtime.NewGoError(err))
		}
		_, _ = hasher.Write(data)
		buffer := runtime.NewArrayBuffer(hasher.Sum(nil))
		return scriptResolvedPromise(runtime, runtime.ToValue(buffer))
	})
	_ = subtle.Set("generateKey", func(call goja.FunctionCall) goja.Value {
		algorithm := scriptWebCryptoAlgorithmValue(runtime, call.Argument(0))
		_ = algorithm.Set("length", scriptWebCryptoAlgorithmLength(runtime, call.Argument(0)))
		key := runtime.NewObject()
		_ = key.Set("type", "secret")
		_ = key.Set("algorithm", algorithm)
		_ = key.Set("extractable", len(call.Arguments) > 1 && call.Argument(1).ToBoolean())
		if len(call.Arguments) > 2 {
			_ = key.Set("usages", call.Argument(2))
		} else {
			_ = key.Set("usages", []string{})
		}
		return scriptResolvedPromise(runtime, key)
	})
	_ = subtle.Set("importKey", func(call goja.FunctionCall) goja.Value {
		key := runtime.NewObject()
		_ = key.Set("type", "secret")
		_ = key.Set("format", call.Argument(0).String())
		_ = key.Set("algorithm", scriptWebCryptoAlgorithmValue(runtime, call.Argument(2)))
		_ = key.Set("extractable", len(call.Arguments) > 3 && call.Argument(3).ToBoolean())
		if len(call.Arguments) > 4 {
			_ = key.Set("usages", call.Argument(4))
		} else {
			_ = key.Set("usages", []string{})
		}
		return scriptResolvedPromise(runtime, key)
	})
	_ = subtle.Set("exportKey", func(call goja.FunctionCall) goja.Value {
		format := ""
		if len(call.Arguments) > 0 {
			format = strings.ToLower(call.Argument(0).String())
		}
		if format == "jwk" {
			key := runtime.NewObject()
			_ = key.Set("kty", "oct")
			_ = key.Set("k", "")
			return scriptResolvedPromise(runtime, key)
		}
		return scriptResolvedPromise(runtime, runtime.ToValue(runtime.NewArrayBuffer(nil)))
	})
	_ = subtle.Set("sign", func(goja.FunctionCall) goja.Value {
		return scriptResolvedPromise(runtime, runtime.ToValue(runtime.NewArrayBuffer(nil)))
	})
	_ = subtle.Set("verify", func(goja.FunctionCall) goja.Value {
		return scriptResolvedPromise(runtime, runtime.ToValue(true))
	})
	_ = subtle.Set("encrypt", func(call goja.FunctionCall) goja.Value {
		data := []byte(nil)
		var err error
		if len(call.Arguments) > 2 {
			data, err = scriptCryptoValueBytes(runtime, call.Argument(2), "")
		}
		if err != nil {
			return scriptRejectedPromise(runtime, runtime.NewGoError(err))
		}
		return scriptResolvedPromise(runtime, runtime.ToValue(runtime.NewArrayBuffer(data)))
	})
	_ = subtle.Set("decrypt", func(call goja.FunctionCall) goja.Value {
		data := []byte(nil)
		var err error
		if len(call.Arguments) > 2 {
			data, err = scriptCryptoValueBytes(runtime, call.Argument(2), "")
		}
		if err != nil {
			return scriptRejectedPromise(runtime, runtime.NewGoError(err))
		}
		return scriptResolvedPromise(runtime, runtime.ToValue(runtime.NewArrayBuffer(data)))
	})
	return subtle
}

func scriptWebCryptoAlgorithmName(runtime *goja.Runtime, value goja.Value) string {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return ""
	}
	if text, ok := value.Export().(string); ok {
		return text
	}
	object := value.ToObject(runtime)
	name := object.Get("name")
	if name == nil || goja.IsUndefined(name) || goja.IsNull(name) {
		return value.String()
	}
	return name.String()
}

func scriptWebCryptoAlgorithmValue(runtime *goja.Runtime, value goja.Value) *goja.Object {
	algorithm := runtime.NewObject()
	_ = algorithm.Set("name", scriptWebCryptoAlgorithmName(runtime, value))
	return algorithm
}

func scriptWebCryptoAlgorithmLength(runtime *goja.Runtime, value goja.Value) int {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return 0
	}
	object := value.ToObject(runtime)
	length := object.Get("length")
	if length == nil || goja.IsUndefined(length) || goja.IsNull(length) {
		return 0
	}
	return int(length.ToInteger())
}

func newScriptCryptoDigestObject(runtime *goja.Runtime, hasher hash.Hash) goja.Value {
	object := runtime.NewObject()
	finalized := false
	_ = object.Set("update", func(call goja.FunctionCall) goja.Value {
		if finalized {
			panic(runtime.NewTypeError("digest already called"))
		}
		encoding := ""
		if len(call.Arguments) > 1 {
			encoding = call.Argument(1).String()
		}
		data, err := scriptCryptoValueBytes(runtime, call.Argument(0), encoding)
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		if _, err := hasher.Write(data); err != nil {
			panic(runtime.NewGoError(err))
		}
		return object
	})
	_ = object.Set("digest", func(call goja.FunctionCall) goja.Value {
		if finalized {
			panic(runtime.NewTypeError("digest already called"))
		}
		finalized = true
		encoding := ""
		if len(call.Arguments) > 0 {
			encoding = call.Argument(0).String()
		}
		return scriptCryptoDigestValue(runtime, hasher.Sum(nil), encoding)
	})
	return object
}

func newScriptCryptoHash(algorithm string) (hash.Hash, error) {
	factory, err := scriptCryptoHashFactory(algorithm)
	if err != nil {
		return nil, err
	}
	return factory(), nil
}

func scriptCryptoHashFactory(algorithm string) (func() hash.Hash, error) {
	switch normalizeScriptCryptoAlgorithm(algorithm) {
	case "md5":
		return md5.New, nil
	case "sha1":
		return sha1.New, nil
	case "sha224":
		return sha256.New224, nil
	case "sha256":
		return sha256.New, nil
	case "sha384":
		return sha512.New384, nil
	case "sha512":
		return sha512.New, nil
	default:
		return nil, fmt.Errorf("unsupported crypto algorithm: %s", algorithm)
	}
}

func normalizeScriptCryptoAlgorithm(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "rsa-")
	value = strings.ReplaceAll(value, "-", "")
	value = strings.ReplaceAll(value, "_", "")
	return value
}

func scriptCryptoValueBytes(runtime *goja.Runtime, value goja.Value, encoding string) ([]byte, error) {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return nil, nil
	}
	encoding = normalizeScriptFSEncoding(encoding)
	if text, ok := value.Export().(string); ok {
		return scriptCryptoStringBytes(text, encoding)
	}
	object := value.ToObject(runtime)
	lengthValue := object.Get("length")
	if lengthValue != nil && !goja.IsUndefined(lengthValue) && !goja.IsNull(lengthValue) {
		length := int(lengthValue.ToInteger())
		if length < 0 {
			return nil, fmt.Errorf("negative byte array length")
		}
		out := make([]byte, 0, length)
		for index := 0; index < length; index++ {
			out = append(out, byte(object.Get(strconv.Itoa(index)).ToInteger()))
		}
		return out, nil
	}
	return scriptCryptoStringBytes(fmt.Sprint(value.Export()), encoding)
}

func scriptCryptoStringBytes(value, encoding string) ([]byte, error) {
	switch normalizeScriptFSEncoding(encoding) {
	case "", "utf8", "utf":
		return []byte(value), nil
	case "hex":
		return hex.DecodeString(value)
	case "base64", "base64url":
		return decodeScriptBase64(value)
	case "latin1", "binary", "ascii":
		return scriptBytesFromBinaryString(value)
	default:
		return nil, fmt.Errorf("unsupported crypto input encoding: %s", encoding)
	}
}

func scriptCryptoDigestValue(runtime *goja.Runtime, data []byte, encoding string) goja.Value {
	switch normalizeScriptFSEncoding(encoding) {
	case "":
		return scriptBufferValue(runtime, data)
	case "hex":
		return runtime.ToValue(hex.EncodeToString(data))
	case "base64", "base64url":
		encoded := base64.StdEncoding.EncodeToString(data)
		if normalizeScriptFSEncoding(encoding) == "base64url" {
			encoded = strings.TrimRight(strings.NewReplacer("+", "-", "/", "_").Replace(encoded), "=")
		}
		return runtime.ToValue(encoded)
	case "latin1", "binary", "ascii":
		return runtime.ToValue(scriptBinaryStringFromBytes(data))
	case "utf8", "utf":
		return runtime.ToValue(string(data))
	default:
		panic(runtime.NewTypeError("unsupported crypto digest encoding: " + encoding))
	}
}

func newScriptCryptoJSObject(runtime *goja.Runtime) *goja.Object {
	native := runtime.NewObject()
	_ = native.Set("hash", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			panic(runtime.NewTypeError("algorithm and data are required"))
		}
		data, err := decodeScriptBase64(call.Argument(1).String())
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		hasher, err := newScriptCryptoHash(call.Argument(0).String())
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		_, _ = hasher.Write(data)
		return runtime.ToValue(base64.StdEncoding.EncodeToString(hasher.Sum(nil)))
	})
	_ = native.Set("hmac", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 3 {
			panic(runtime.NewTypeError("algorithm, data, and key are required"))
		}
		data, err := decodeScriptBase64(call.Argument(1).String())
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		key, err := decodeScriptBase64(call.Argument(2).String())
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		factory, err := scriptCryptoHashFactory(call.Argument(0).String())
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		hasher := hmac.New(factory, key)
		_, _ = hasher.Write(data)
		return runtime.ToValue(base64.StdEncoding.EncodeToString(hasher.Sum(nil)))
	})
	_ = native.Set("aesEncrypt", func(message, passphrase string) goja.Value {
		ciphertext, err := scriptCryptoJSAESEncrypt([]byte(message), []byte(passphrase))
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		return runtime.ToValue(ciphertext)
	})
	_ = native.Set("aesDecrypt", func(ciphertext, passphrase string) goja.Value {
		plaintext, err := scriptCryptoJSAESDecrypt(ciphertext, []byte(passphrase))
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		return runtime.ToValue(base64.StdEncoding.EncodeToString(plaintext))
	})
	script := `(function (native) {
  const enc = {};

  function normalizeBase64(value) {
    let text = String(value || "").replace(/-/g, "+").replace(/_/g, "/");
    while (text.length % 4) {
      text += "=";
    }
    return text;
  }

  class WordArray {
    constructor(base64) {
      Object.defineProperty(this, "__liteApiCryptoJSWordArray", { value: true, enumerable: false });
      Object.defineProperty(this, "__base64", { value: normalizeBase64(base64), enumerable: false, writable: true });
      this.sigBytes = Buffer.from(this.__base64, "base64").length;
      this.words = bytesToWords(Buffer.from(this.__base64, "base64"));
    }
    toString(encoder) {
      const bytes = Buffer.from(this.__base64, "base64");
      if (encoder === enc.Base64) {
        return this.__base64;
      }
      if (encoder === enc.Utf8) {
        return bytes.toString("utf8");
      }
      if (encoder === enc.Latin1) {
        return bytes.toString("latin1");
      }
      return bytes.toString("hex");
    }
    concat(other) {
      const left = Buffer.from(this.__base64, "base64");
      const right = Buffer.from(base64FromValue(other), "base64");
      this.__base64 = Buffer.concat([left, right]).toString("base64");
      this.sigBytes = left.length + right.length;
      this.words = bytesToWords(Buffer.from(this.__base64, "base64"));
      return this;
    }
    clone() {
      return new WordArray(this.__base64);
    }
  }

  function bytesToWords(bytes) {
    const words = [];
    for (let index = 0; index < bytes.length; index += 4) {
      words.push(((bytes[index] || 0) << 24) | ((bytes[index + 1] || 0) << 16) | ((bytes[index + 2] || 0) << 8) | (bytes[index + 3] || 0));
    }
    return words;
  }

  function wordArrayFromWords(words, sigBytes) {
    const bytes = [];
    const total = sigBytes === undefined ? words.length * 4 : Number(sigBytes);
    for (let index = 0; index < words.length && bytes.length < total; index++) {
      const word = Number(words[index]) >>> 0;
      bytes.push((word >>> 24) & 255);
      if (bytes.length < total) bytes.push((word >>> 16) & 255);
      if (bytes.length < total) bytes.push((word >>> 8) & 255);
      if (bytes.length < total) bytes.push(word & 255);
    }
    return new WordArray(Buffer.from(bytes).toString("base64"));
  }

  function isWordArray(value) {
    return !!(value && value.__liteApiCryptoJSWordArray === true);
  }

  function base64FromValue(value) {
    if (isWordArray(value)) {
      return value.__base64;
    }
    if (Buffer.isBuffer(value) || value instanceof Uint8Array || value instanceof ArrayBuffer || Array.isArray(value)) {
      return Buffer.from(value).toString("base64");
    }
    return Buffer.from(String(value), "utf8").toString("base64");
  }

  function stringFromValue(value) {
    if (isWordArray(value)) {
      return value.toString(enc.Utf8);
    }
    return String(value);
  }

  enc.Hex = {
    parse: function (value) { return new WordArray(Buffer.from(String(value), "hex").toString("base64")); },
    stringify: function (value) { return new WordArray(base64FromValue(value)).toString(enc.Hex); }
  };
  enc.Base64 = {
    parse: function (value) { return new WordArray(normalizeBase64(value)); },
    stringify: function (value) { return new WordArray(base64FromValue(value)).toString(enc.Base64); }
  };
  enc.Utf8 = {
    parse: function (value) { return new WordArray(Buffer.from(String(value), "utf8").toString("base64")); },
    stringify: function (value) { return new WordArray(base64FromValue(value)).toString(enc.Utf8); }
  };
  enc.Latin1 = {
    parse: function (value) { return new WordArray(Buffer.from(String(value), "latin1").toString("base64")); },
    stringify: function (value) { return new WordArray(base64FromValue(value)).toString(enc.Latin1); }
  };

  function hashFunction(algorithm) {
    return function (value) {
      return new WordArray(native.hash(algorithm, base64FromValue(value)));
    };
  }

  function hmacFunction(algorithm) {
    return function (value, key) {
      return new WordArray(native.hmac(algorithm, base64FromValue(value), base64FromValue(key)));
    };
  }

  const CryptoJS = {
    enc,
    lib: {
      WordArray: {
        create: function (words, sigBytes) {
          if (words === undefined || words === null) {
            return new WordArray("");
          }
          if (isWordArray(words)) {
            return words.clone();
          }
          if (Array.isArray(words)) {
            return wordArrayFromWords(words, sigBytes);
          }
          return new WordArray(base64FromValue(words));
        },
        random: function (size) {
          return new WordArray(crypto.randomBytes(Number(size) || 0).toString("base64"));
        }
      }
    },
    AES: {
      encrypt: function (message, passphrase) {
        const value = native.aesEncrypt(stringFromValue(message), stringFromValue(passphrase));
        return {
          toString: function () { return value; }
        };
      },
      decrypt: function (ciphertext, passphrase) {
        const value = typeof ciphertext === "string" ? ciphertext : ciphertext.toString();
        return new WordArray(native.aesDecrypt(value, stringFromValue(passphrase)));
      }
    },
    MD5: hashFunction("md5"),
    SHA1: hashFunction("sha1"),
    SHA224: hashFunction("sha224"),
    SHA256: hashFunction("sha256"),
    SHA384: hashFunction("sha384"),
    SHA512: hashFunction("sha512"),
    HmacMD5: hmacFunction("md5"),
    HmacSHA1: hmacFunction("sha1"),
    HmacSHA224: hmacFunction("sha224"),
    HmacSHA256: hmacFunction("sha256"),
    HmacSHA384: hmacFunction("sha384"),
    HmacSHA512: hmacFunction("sha512")
  };
  return CryptoJS;
})`
	value, err := runtime.RunProgram(scriptCryptoJSShim.compiled(script))
	if err != nil {
		panic(runtime.NewGoError(err))
	}
	fn, ok := goja.AssertFunction(value)
	if !ok {
		panic(runtime.NewTypeError("crypto-js factory is not callable"))
	}
	result, err := fn(goja.Undefined(), native)
	if err != nil {
		panic(err)
	}
	return result.ToObject(runtime)
}

func scriptCryptoJSAESEncrypt(plaintext, passphrase []byte) (string, error) {
	if len(passphrase) == 0 {
		return "", errors.New("crypto-js passphrase is required")
	}
	salt := make([]byte, 8)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, iv := scriptCryptoJSEvpBytesToKey(passphrase, salt)
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	padded := ScriptPKCS7Pad(plaintext, block.BlockSize())
	ciphertext := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ciphertext, padded)
	output := make([]byte, 0, 16+len(ciphertext))
	output = append(output, []byte("Salted__")...)
	output = append(output, salt...)
	output = append(output, ciphertext...)
	return base64.StdEncoding.EncodeToString(output), nil
}

func scriptCryptoJSAESDecrypt(ciphertext string, passphrase []byte) ([]byte, error) {
	if len(passphrase) == 0 {
		return nil, errors.New("crypto-js passphrase is required")
	}
	raw, err := decodeScriptBase64(strings.TrimSpace(ciphertext))
	if err != nil {
		return nil, err
	}
	if len(raw) < 16 || string(raw[:8]) != "Salted__" {
		return nil, errors.New("unsupported crypto-js AES ciphertext")
	}
	salt := raw[8:16]
	encrypted := raw[16:]
	if len(encrypted) == 0 || len(encrypted)%aes.BlockSize != 0 {
		return nil, errors.New("invalid crypto-js AES ciphertext")
	}
	key, iv := scriptCryptoJSEvpBytesToKey(passphrase, salt)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	plaintext := make([]byte, len(encrypted))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plaintext, encrypted)
	return ScriptPKCS7Unpad(plaintext, block.BlockSize())
}

func scriptCryptoJSEvpBytesToKey(passphrase, salt []byte) ([]byte, []byte) {
	out := make([]byte, 0, 48)
	var previous []byte
	for len(out) < 48 {
		hasher := md5.New()
		_, _ = hasher.Write(previous)
		_, _ = hasher.Write(passphrase)
		_, _ = hasher.Write(salt)
		previous = hasher.Sum(nil)
		out = append(out, previous...)
	}
	return out[:32], out[32:48]
}
