// Package sealed carries a device's requests to the server through a relay
// that must not read them, such as a Cloudflare Tunnel. The device locks
// each request to the server's public key and the server locks the answer to
// a key only that request's sender holds, so the relay sees sizes and timing
// but never the data, the path or the device token.
//
// A request is version (1 byte), the sender's one-time P-256 public key (65
// bytes, uncompressed), a nonce (12 bytes) and AES-256-GCM ciphertext; the
// first 66 bytes are authenticated with it. Both keys come from
// HKDF-SHA256 over the ECDH secret, salted with both public keys. An answer
// is version, nonce and ciphertext under the answer key.
//
// Inside, a request is a JSON head, a newline and the raw body; an answer is
// the same with the status and content type in the head.
package sealed

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

const (
	version   = 1
	pointSize = 65
	nonceSize = 12
	info      = "telemetry sealed v1"
)

// Head is what a request says about itself, inside the seal.
type Head struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers,omitempty"`
	// At is when the request was sealed, in Unix milliseconds, and ID is
	// random: together they stop a copied request from being sent again.
	At int64  `json:"at"`
	ID string `json:"id"`
}

// AnswerHead is what an answer says about itself, inside the seal.
type AnswerHead struct {
	Status      int    `json:"status"`
	ContentType string `json:"content_type,omitempty"`
}

// ServerKey is the server's long-term key.
type ServerKey struct{ private *ecdh.PrivateKey }

// LoadOrCreateKey reads the server key from path, creating it (owner-only)
// when the file does not exist yet.
func LoadOrCreateKey(path string) (*ServerKey, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		private, err := ecdh.P256().GenerateKey(rand.Reader)
		if err != nil {
			return nil, err
		}
		encoded := base64.StdEncoding.EncodeToString(private.Bytes()) + "\n"
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return nil, err
		}
		if _, err := file.WriteString(encoded); err != nil {
			file.Close()
			return nil, err
		}
		if err := file.Close(); err != nil {
			return nil, err
		}
		return &ServerKey{private}, nil
	}
	if err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
	if err != nil {
		return nil, fmt.Errorf("sealed key %s: %w", path, err)
	}
	private, err := ecdh.P256().NewPrivateKey(raw)
	if err != nil {
		return nil, fmt.Errorf("sealed key %s: %w", path, err)
	}
	return &ServerKey{private}, nil
}

// Public is the public key devices are built with, base64 of the
// uncompressed point.
func (k *ServerKey) Public() string {
	return base64.StdEncoding.EncodeToString(k.private.PublicKey().Bytes())
}

// ParsePublic reads a public key written by Public.
func ParsePublic(encoded string) (*ecdh.PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return nil, err
	}
	return ecdh.P256().NewPublicKey(raw)
}

func keys(secret, sender, server []byte) (request, answer cipher.AEAD, err error) {
	salt := append(append([]byte{}, sender...), server...)
	material, err := hkdf.Key(sha256.New, secret, salt, info, 64)
	if err != nil {
		return nil, nil, err
	}
	if request, err = aead(material[:32]); err != nil {
		return nil, nil, err
	}
	answer, err = aead(material[32:])
	return request, answer, err
}

func aead(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// ErrSeal is any request or answer that cannot be opened.
var ErrSeal = errors.New("cannot open sealed message")

// Opened is a request the server opened, with the key for its answer.
type Opened struct {
	Head   Head
	Body   []byte
	answer cipher.AEAD
}

// Open unlocks a request sealed to this server.
func (k *ServerKey) Open(message []byte) (*Opened, error) {
	if len(message) < 1+pointSize+nonceSize+16 || message[0] != version {
		return nil, ErrSeal
	}
	sender, err := ecdh.P256().NewPublicKey(message[1 : 1+pointSize])
	if err != nil {
		return nil, ErrSeal
	}
	secret, err := k.private.ECDH(sender)
	if err != nil {
		return nil, ErrSeal
	}
	request, answer, err := keys(secret, sender.Bytes(), k.private.PublicKey().Bytes())
	if err != nil {
		return nil, err
	}
	header := message[:1+pointSize]
	nonce := message[1+pointSize : 1+pointSize+nonceSize]
	plain, err := request.Open(nil, nonce, message[1+pointSize+nonceSize:], header)
	if err != nil {
		return nil, ErrSeal
	}
	head, body, err := split(plain)
	if err != nil {
		return nil, ErrSeal
	}
	opened := &Opened{Body: body, answer: answer}
	if err := json.Unmarshal(head, &opened.Head); err != nil {
		return nil, ErrSeal
	}
	return opened, nil
}

// Answer seals the answer to this request.
func (o *Opened) Answer(head AnswerHead, body []byte) ([]byte, error) {
	return seal(o.answer, []byte{version}, head, body)
}

func seal(key cipher.AEAD, prefix []byte, head any, body []byte) ([]byte, error) {
	encoded, err := json.Marshal(head)
	if err != nil {
		return nil, err
	}
	plain := append(append(encoded, '\n'), body...)
	nonce := make([]byte, nonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := append(append([]byte{}, prefix...), nonce...)
	return key.Seal(out, nonce, plain, prefix), nil
}

func split(plain []byte) (head, body []byte, err error) {
	index := bytes.IndexByte(plain, '\n')
	if index < 0 {
		return nil, nil, ErrSeal
	}
	return plain[:index], plain[index+1:], nil
}

// Sealer is a device's side: it seals requests and opens their answers.
type Sealer struct{ server *ecdh.PublicKey }

func NewSealer(server *ecdh.PublicKey) *Sealer { return &Sealer{server} }

// Seal locks a request; open its answer with the returned function.
func (s *Sealer) Seal(head Head, body []byte) ([]byte, func([]byte) (AnswerHead, []byte, error), error) {
	private, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	secret, err := private.ECDH(s.server)
	if err != nil {
		return nil, nil, err
	}
	sender := private.PublicKey().Bytes()
	request, answer, err := keys(secret, sender, s.server.Bytes())
	if err != nil {
		return nil, nil, err
	}
	message, err := seal(request, append([]byte{version}, sender...), head, body)
	if err != nil {
		return nil, nil, err
	}
	open := func(sealed []byte) (AnswerHead, []byte, error) {
		var head AnswerHead
		if len(sealed) < 1+nonceSize+16 || sealed[0] != version {
			return head, nil, ErrSeal
		}
		plain, err := answer.Open(nil, sealed[1:1+nonceSize], sealed[1+nonceSize:], sealed[:1])
		if err != nil {
			return head, nil, ErrSeal
		}
		encoded, body, err := split(plain)
		if err != nil || json.Unmarshal(encoded, &head) != nil {
			return head, nil, ErrSeal
		}
		return head, body, nil
	}
	return message, open, nil
}
