package scripting

// The sandbox AES-CBC cipher object and PKCS#7 padding.
//
// Split out of scripting.go by AST: declarations are identified by the parser
// and copied verbatim from their source offsets.

import (
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"fmt"

	"github.com/dop251/goja"
)

type scriptAESCBCState struct {
	block       cipher.Block
	iv          []byte
	encrypt     bool
	autoPadding bool
	pending     []byte
	finalized   bool
}

func newScriptAESCBCObject(runtime *goja.Runtime, call goja.FunctionCall, encrypt bool) goja.Value {
	if len(call.Arguments) < 3 {
		panic(runtime.NewTypeError("algorithm, key, and iv are required"))
	}
	keyLength, err := scriptAESCBCKeyLength(call.Argument(0).String())
	if err != nil {
		panic(runtime.NewGoError(err))
	}
	key, err := scriptCryptoValueBytes(runtime, call.Argument(1), "")
	if err != nil {
		panic(runtime.NewGoError(err))
	}
	if len(key) != keyLength {
		panic(runtime.NewTypeError(fmt.Sprintf("Invalid key length: expected %d bytes", keyLength)))
	}
	iv, err := scriptCryptoValueBytes(runtime, call.Argument(2), "")
	if err != nil {
		panic(runtime.NewGoError(err))
	}
	if len(iv) != aes.BlockSize {
		panic(runtime.NewTypeError(fmt.Sprintf("Invalid initialization vector length: expected %d bytes", aes.BlockSize)))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		panic(runtime.NewGoError(err))
	}
	state := &scriptAESCBCState{
		block:       block,
		iv:          append([]byte(nil), iv...),
		encrypt:     encrypt,
		autoPadding: true,
	}
	object := runtime.NewObject()
	_ = object.Set("update", func(call goja.FunctionCall) goja.Value {
		if state.finalized {
			panic(runtime.NewTypeError("cipher already finalized"))
		}
		inputEncoding := ""
		outputEncoding := ""
		if len(call.Arguments) > 1 {
			inputEncoding = call.Argument(1).String()
		}
		if len(call.Arguments) > 2 {
			outputEncoding = call.Argument(2).String()
		}
		data, err := scriptCryptoValueBytes(runtime, call.Argument(0), inputEncoding)
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		state.pending = append(state.pending, data...)
		return scriptCryptoDigestValue(runtime, nil, outputEncoding)
	})
	_ = object.Set("final", func(call goja.FunctionCall) goja.Value {
		if state.finalized {
			panic(runtime.NewTypeError("cipher already finalized"))
		}
		state.finalized = true
		outputEncoding := ""
		if len(call.Arguments) > 0 {
			outputEncoding = call.Argument(0).String()
		}
		var out []byte
		var err error
		if state.encrypt {
			out, err = state.encryptFinal()
		} else {
			out, err = state.decryptFinal()
		}
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		return scriptCryptoDigestValue(runtime, out, outputEncoding)
	})
	_ = object.Set("setAutoPadding", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			state.autoPadding = true
		} else {
			state.autoPadding = call.Argument(0).ToBoolean()
		}
		return object
	})
	return object
}

func scriptAESCBCKeyLength(algorithm string) (int, error) {
	switch normalizeScriptCryptoAlgorithm(algorithm) {
	case "aes128cbc":
		return 16, nil
	case "aes192cbc":
		return 24, nil
	case "aes256cbc":
		return 32, nil
	default:
		return 0, fmt.Errorf("unsupported cipher algorithm: %s", algorithm)
	}
}

func (state *scriptAESCBCState) encryptFinal() ([]byte, error) {
	data := append([]byte(nil), state.pending...)
	if state.autoPadding {
		data = ScriptPKCS7Pad(data, state.block.BlockSize())
	} else if len(data)%state.block.BlockSize() != 0 {
		return nil, errors.New("data is not a multiple of block length")
	}
	out := make([]byte, len(data))
	cipher.NewCBCEncrypter(state.block, state.iv).CryptBlocks(out, data)
	return out, nil
}

func (state *scriptAESCBCState) decryptFinal() ([]byte, error) {
	if len(state.pending)%state.block.BlockSize() != 0 {
		return nil, errors.New("encrypted data is not a multiple of block length")
	}
	out := make([]byte, len(state.pending))
	cipher.NewCBCDecrypter(state.block, state.iv).CryptBlocks(out, state.pending)
	if state.autoPadding {
		return ScriptPKCS7Unpad(out, state.block.BlockSize())
	}
	return out, nil
}

func ScriptPKCS7Pad(data []byte, blockSize int) []byte {
	padding := blockSize - len(data)%blockSize
	if padding == 0 {
		padding = blockSize
	}
	out := make([]byte, len(data)+padding)
	copy(out, data)
	for index := len(data); index < len(out); index++ {
		out[index] = byte(padding)
	}
	return out
}

func ScriptPKCS7Unpad(data []byte, blockSize int) ([]byte, error) {
	if len(data) == 0 || len(data)%blockSize != 0 {
		return nil, errors.New("invalid PKCS#7 data")
	}
	padding := int(data[len(data)-1])
	if padding == 0 || padding > blockSize || padding > len(data) {
		return nil, errors.New("invalid PKCS#7 padding")
	}
	for _, value := range data[len(data)-padding:] {
		if int(value) != padding {
			return nil, errors.New("invalid PKCS#7 padding")
		}
	}
	return data[:len(data)-padding], nil
}
