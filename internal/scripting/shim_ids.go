package scripting

// The sandbox uuid and nanoid shims.
//
// Split out of scripting.go by AST: declarations are identified by the parser
// and copied verbatim from their source offsets.

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"

	googleuuid "github.com/google/uuid"

	"github.com/dop251/goja"
)

func newScriptUUIDObject(runtime *goja.Runtime) *goja.Object {
	uuidObject := runtime.NewObject()
	mustString := func(id googleuuid.UUID, err error) string {
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		return id.String()
	}
	nameBased := func(factory func(googleuuid.UUID, []byte) googleuuid.UUID) func(string, string) string {
		return func(name, namespace string) string {
			space, err := googleuuid.Parse(namespace)
			if err != nil {
				panic(runtime.NewGoError(err))
			}
			return factory(space, []byte(name)).String()
		}
	}
	v3 := runtime.ToValue(nameBased(googleuuid.NewMD5))
	_ = v3.ToObject(runtime).Set("DNS", googleuuid.NameSpaceDNS.String())
	_ = v3.ToObject(runtime).Set("URL", googleuuid.NameSpaceURL.String())
	v5 := runtime.ToValue(nameBased(googleuuid.NewSHA1))
	_ = v5.ToObject(runtime).Set("DNS", googleuuid.NameSpaceDNS.String())
	_ = v5.ToObject(runtime).Set("URL", googleuuid.NameSpaceURL.String())
	_ = uuidObject.Set("NIL", googleuuid.Nil.String())
	_ = uuidObject.Set("MAX", "ffffffff-ffff-ffff-ffff-ffffffffffff")
	_ = uuidObject.Set("v1", func() string { return mustString(googleuuid.NewUUID()) })
	_ = uuidObject.Set("v3", v3)
	_ = uuidObject.Set("v4", func() string { return mustString(googleuuid.NewRandom()) })
	_ = uuidObject.Set("v5", v5)
	_ = uuidObject.Set("v6", func() string { return mustString(googleuuid.NewV6()) })
	_ = uuidObject.Set("v7", func() string { return mustString(googleuuid.NewV7()) })
	_ = uuidObject.Set("v1ToV6", func(value string) string {
		id, err := googleuuid.Parse(value)
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		return scriptUUIDV1ToV6(id).String()
	})
	_ = uuidObject.Set("v6ToV1", func(value string) string {
		id, err := googleuuid.Parse(value)
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		return scriptUUIDV6ToV1(id).String()
	})
	_ = uuidObject.Set("validate", func(value string) bool {
		_, err := googleuuid.Parse(value)
		return err == nil
	})
	_ = uuidObject.Set("version", func(value string) int {
		id, err := googleuuid.Parse(value)
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		return int(id.Version())
	})
	_ = uuidObject.Set("parse", func(value string) goja.Value {
		id, err := googleuuid.Parse(value)
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		return scriptByteArrayValue(runtime, id[:])
	})
	_ = uuidObject.Set("stringify", func(call goja.FunctionCall) goja.Value {
		data, err := scriptCryptoValueBytes(runtime, call.Argument(0), "")
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		if len(data) != 16 {
			panic(runtime.NewGoError(fmt.Errorf("uuid byte array must contain 16 bytes")))
		}
		var id googleuuid.UUID
		copy(id[:], data)
		return runtime.ToValue(id.String())
	})
	return uuidObject
}

func scriptUUIDV1ToV6(id googleuuid.UUID) googleuuid.UUID {
	timestamp := (uint64(binary.BigEndian.Uint16(id[6:8])&0x0fff) << 48) |
		(uint64(binary.BigEndian.Uint16(id[4:6])) << 32) |
		uint64(binary.BigEndian.Uint32(id[0:4]))
	var out googleuuid.UUID
	binary.BigEndian.PutUint64(out[0:8], timestamp)
	out[6] = 0x60 | (out[6] & 0x0f)
	copy(out[8:], id[8:])
	out[8] = 0x80 | (out[8] & 0x3f)
	return out
}

func scriptUUIDV6ToV1(id googleuuid.UUID) googleuuid.UUID {
	ordered := id
	ordered[6] &= 0x0f
	timestamp := binary.BigEndian.Uint64(ordered[0:8])
	var out googleuuid.UUID
	binary.BigEndian.PutUint32(out[0:4], uint32(timestamp))
	binary.BigEndian.PutUint16(out[4:6], uint16(timestamp>>32))
	binary.BigEndian.PutUint16(out[6:8], uint16(timestamp>>48))
	out[6] = 0x10 | (out[6] & 0x0f)
	copy(out[8:], id[8:])
	out[8] = 0x80 | (out[8] & 0x3f)
	return out
}

func newScriptNanoIDObject(runtime *goja.Runtime) *goja.Object {
	nanoidObject := runtime.NewObject()
	_ = nanoidObject.Set("nanoid", func(call goja.FunctionCall) goja.Value {
		size := 21
		if len(call.Arguments) > 0 && !goja.IsUndefined(call.Argument(0)) && !goja.IsNull(call.Argument(0)) {
			size = int(call.Argument(0).ToInteger())
		}
		return runtime.ToValue(scriptNanoID(size))
	})
	return nanoidObject
}

func scriptRandomUUID() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		panic(err)
	}
	bytes[6] = (bytes[6] & 0x0f) | 0x40
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	hexed := hex.EncodeToString(bytes)
	return hexed[0:8] + "-" + hexed[8:12] + "-" + hexed[12:16] + "-" + hexed[16:20] + "-" + hexed[20:32]
}

func scriptNanoID(size int) string {
	if size < 0 {
		size = 0
	}
	const alphabet = "_-0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	bytes := make([]byte, size)
	if _, err := rand.Read(bytes); err != nil {
		panic(err)
	}
	var builder strings.Builder
	builder.Grow(size)
	for _, value := range bytes {
		builder.WriteByte(alphabet[int(value)%len(alphabet)])
	}
	return builder.String()
}
