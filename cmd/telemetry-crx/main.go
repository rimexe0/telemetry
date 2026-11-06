// telemetry-crx packs the Telemetry browser extension as a signed CRX3 file
// for the release, with the RSA key in TELEMETRY_EXTENSION_KEY (PKCS #8
// PEM), and prints its extension ID. Browser policy installs it from the
// collector on Linux.
package main

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/rimexe0/telemetry/internal/crx"
)

func main() {
	dir := flag.String("dir", "extensions/telemetry-blocker", "the extension's folder")
	out := flag.String("out", "telemetry-extension.crx", "where to write the packed extension")
	flag.Parse()
	block, _ := pem.Decode([]byte(os.Getenv("TELEMETRY_EXTENSION_KEY")))
	if block == nil {
		log.Fatal("TELEMETRY_EXTENSION_KEY holds no PEM key")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		log.Fatal(err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		log.Fatal("the extension key is not RSA")
	}
	archive, err := crx.Zip(*dir, []string{"manifest.json", "background.js", "cover.js", "popup.html", "popup.js"})
	if err != nil {
		log.Fatal(err)
	}
	packed, err := crx.Pack(archive, key)
	if err != nil {
		log.Fatal(err)
	}
	id, err := crx.Verify(packed)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(*out, packed, 0o644); err != nil {
		log.Fatal(err)
	}
	fmt.Println(id)
}
