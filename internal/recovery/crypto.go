package recovery

// Encrypting a recovery snapshot at rest, bound to the workspace it belongs to.
//
// Split out by AST: declarations are identified by the parser and copied
// verbatim from their source offsets.

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/mutexdev/lite_api/internal/secretkey"
)

func recoveryAAD(dataDir, workspaceID, artifact string) []byte {
	return []byte("liteapi-recovery/v1\x00" + filepath.Clean(dataDir) + "\x00" + workspaceID + "\x00" + artifact)
}

func encryptRecovery(dataDir, workspaceID, artifact string, plain []byte) ([]byte, error) {
	block, err := aes.NewCipher(secretkey.AESKey(dataDir))
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	envelope := recoveryEnvelope{Version: recoveryEnvelopeVersion, Algorithm: "AES-256-GCM", Nonce: base64.RawStdEncoding.EncodeToString(nonce), Ciphertext: base64.RawStdEncoding.EncodeToString(gcm.Seal(nil, nonce, plain, recoveryAAD(dataDir, workspaceID, artifact)))}
	return json.Marshal(envelope)
}

func decryptRecovery(dataDir, workspaceID, artifact string, raw []byte) ([]byte, error) {
	var envelope recoveryEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("parse recovery envelope: %w", err)
	}
	if envelope.Version != recoveryEnvelopeVersion || envelope.Algorithm != "AES-256-GCM" {
		return nil, errors.New("unsupported recovery envelope")
	}
	nonce, err := base64.RawStdEncoding.DecodeString(envelope.Nonce)
	if err != nil {
		return nil, errors.New("invalid recovery nonce")
	}
	ciphertext, err := base64.RawStdEncoding.DecodeString(envelope.Ciphertext)
	if err != nil {
		return nil, errors.New("invalid recovery ciphertext")
	}
	block, err := aes.NewCipher(secretkey.AESKey(dataDir))
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(nonce) != gcm.NonceSize() {
		return nil, errors.New("invalid recovery nonce")
	}
	plain, err := gcm.Open(nil, nonce, ciphertext, recoveryAAD(dataDir, workspaceID, artifact))
	if err != nil {
		return nil, errors.New("recovery authentication failed")
	}
	return plain, nil
}
