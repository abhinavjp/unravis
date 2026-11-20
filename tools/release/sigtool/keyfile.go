package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
)

// The private key is stored encrypted with a passphrase: PBKDF2-SHA256 (600,000 rounds) then AES-256-GCM.
// Everything comes from the Go standard library, so there is nothing to buy or install.
const (
	keyFileVersion = 1
	pbkdf2Rounds   = 600000
	minPassphrase  = 12
)

type keyFile struct {
	Version int    `json:"v"`
	Role    string `json:"role"` // stable or rc
	Pub     []byte `json:"pub"`
	Salt    []byte `json:"salt"`
	Rounds  int    `json:"rounds"`
	Nonce   []byte `json:"nonce"`
	Sealed  []byte `json:"sealed"`
}

func deriveKey(pass string, salt []byte, rounds int) ([]byte, error) {
	return pbkdf2.Key(sha256.New, pass, salt, rounds, 32)
}

func sealKey(role string, priv ed25519.PrivateKey, pass string, rounds int) ([]byte, error) {
	if len(pass) < minPassphrase {
		return nil, fmt.Errorf("passphrase must be at least %d characters", minPassphrase)
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	k, err := deriveKey(pass, salt, rounds)
	if err != nil {
		return nil, err
	}
	blk, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(blk)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	pub := priv.Public().(ed25519.PublicKey)
	kf := keyFile{Version: keyFileVersion, Role: role, Pub: pub, Salt: salt, Rounds: rounds, Nonce: nonce}
	// The role and public key are bound in as extra data, so they cannot be edited without breaking the file.
	kf.Sealed = gcm.Seal(nil, nonce, priv.Seed(), []byte(role+"|"+string(pub)))
	return json.MarshalIndent(kf, "", "  ")
}

func openKey(data []byte, pass string) (role string, priv ed25519.PrivateKey, err error) {
	var kf keyFile
	if err := json.Unmarshal(data, &kf); err != nil || kf.Version != keyFileVersion {
		return "", nil, errors.New("not an unravis key file")
	}
	if kf.Rounds < 100000 || kf.Rounds > 10000000 || len(kf.Salt) != 16 || len(kf.Pub) != ed25519.PublicKeySize {
		return "", nil, errors.New("key file is damaged")
	}
	k, err := deriveKey(pass, kf.Salt, kf.Rounds)
	if err != nil {
		return "", nil, err
	}
	blk, err := aes.NewCipher(k)
	if err != nil {
		return "", nil, err
	}
	gcm, err := cipher.NewGCM(blk)
	if err != nil || len(kf.Nonce) != gcm.NonceSize() {
		return "", nil, errors.New("key file is damaged")
	}
	seed, err := gcm.Open(nil, kf.Nonce, kf.Sealed, []byte(kf.Role+"|"+string(kf.Pub)))
	if err != nil {
		return "", nil, errors.New("wrong passphrase, or the key file was changed")
	}
	priv = ed25519.NewKeyFromSeed(seed)
	if string(priv.Public().(ed25519.PublicKey)) != string(kf.Pub) {
		return "", nil, errors.New("key file public key does not match")
	}
	return kf.Role, priv, nil
}
