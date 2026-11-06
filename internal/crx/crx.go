// Package crx packs a browser extension as a CRX3 file, the signed format
// Chromium installs from an update address. Browser policy installs the
// Telemetry extension this way on Linux, where it cannot then be turned off.
package crx

import (
	"archive/zip"
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// ID is the extension ID Chromium derives from the signing key: the first
// 16 bytes of the SHA-256 of its public key, each half byte written as a
// letter from a to p.
func ID(key *rsa.PublicKey) (string, error) {
	der, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		return "", err
	}
	return idOf(der), nil
}

func idOf(der []byte) string {
	sum := sha256.Sum256(der)
	id := make([]byte, 32)
	for i, b := range sum[:16] {
		id[2*i], id[2*i+1] = 'a'+b>>4, 'a'+b&15
	}
	return string(id)
}

// Zip archives the named files from dir, as the extension's root.
func Zip(dir string, names []string) ([]byte, error) {
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		file, err := writer.Create(name)
		if err != nil {
			return nil, err
		}
		if _, err := file.Write(data); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return archive.Bytes(), nil
}

// Pack signs archive, a zip of the extension, with key.
func Pack(archive []byte, key *rsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(der)
	// SignedData { bytes crx_id = 1; }
	signed := field(1, sum[:16])
	message := signedMessage(signed, archive)
	digest := sha256.Sum256(message)
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return nil, err
	}
	// CrxFileHeader { repeated AsymmetricKeyProof sha256_with_rsa = 2;
	// bytes signed_header_data = 10000; }, with AsymmetricKeyProof
	// { bytes public_key = 1; bytes signature = 2; }.
	header := append(field(2, append(field(1, der), field(2, signature)...)), field(10000, signed)...)
	var out bytes.Buffer
	out.WriteString("Cr24")
	binary.Write(&out, binary.LittleEndian, uint32(3))
	binary.Write(&out, binary.LittleEndian, uint32(len(header)))
	out.Write(header)
	out.Write(archive)
	return out.Bytes(), nil
}

// signedMessage is what the signature covers.
func signedMessage(signed, archive []byte) []byte {
	var message bytes.Buffer
	message.WriteString("CRX3 SignedData\x00")
	binary.Write(&message, binary.LittleEndian, uint32(len(signed)))
	message.Write(signed)
	message.Write(archive)
	return message.Bytes()
}

// Verify checks a CRX3 file's RSA signature and returns its extension ID.
func Verify(data []byte) (string, error) {
	if len(data) < 12 || string(data[:4]) != "Cr24" || binary.LittleEndian.Uint32(data[4:]) != 3 {
		return "", errors.New("not a CRX3 file")
	}
	size := int(binary.LittleEndian.Uint32(data[8:]))
	if 12+size > len(data) {
		return "", errors.New("CRX header is cut short")
	}
	header, archive := data[12:12+size], data[12+size:]
	fields, err := parse(header)
	if err != nil {
		return "", err
	}
	signed := fields[10000]
	if len(fields[2]) == 0 || len(signed) != 1 {
		return "", errors.New("CRX header has no RSA proof or signed data")
	}
	proof, err := parse(fields[2][0])
	if err != nil || len(proof[1]) != 1 || len(proof[2]) != 1 {
		return "", errors.New("CRX RSA proof is malformed")
	}
	der := proof[1][0]
	parsed, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return "", err
	}
	key, ok := parsed.(*rsa.PublicKey)
	if !ok {
		return "", errors.New("CRX key is not RSA")
	}
	digest := sha256.Sum256(signedMessage(signed[0], archive))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], proof[2][0]); err != nil {
		return "", fmt.Errorf("CRX signature: %w", err)
	}
	if _, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive))); err != nil {
		return "", fmt.Errorf("CRX archive: %w", err)
	}
	return idOf(der), nil
}

// field encodes a length-delimited protocol buffer field.
func field(number int, value []byte) []byte {
	out := binary.AppendUvarint(nil, uint64(number)<<3|2)
	out = binary.AppendUvarint(out, uint64(len(value)))
	return append(out, value...)
}

// parse reads length-delimited protocol buffer fields, the only kind CRX
// headers use.
func parse(data []byte) (map[int][][]byte, error) {
	fields := map[int][][]byte{}
	reader := bytes.NewReader(data)
	for reader.Len() > 0 {
		tag, err := binary.ReadUvarint(reader)
		if err != nil {
			return nil, err
		}
		if tag&7 != 2 {
			return nil, fmt.Errorf("unexpected field type %d", tag&7)
		}
		size, err := binary.ReadUvarint(reader)
		if err != nil || size > uint64(reader.Len()) {
			return nil, errors.New("field is cut short")
		}
		value := make([]byte, size)
		if _, err := io.ReadFull(reader, value); err != nil {
			return nil, err
		}
		fields[int(tag>>3)] = append(fields[int(tag>>3)], value)
	}
	return fields, nil
}
