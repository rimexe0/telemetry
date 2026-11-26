// telemetry-xpi packs the Telemetry browser extension for Firefox and its
// forks, such as LibreWolf, for the release. With Mozilla signing
// credentials in AMO_JWT_ISSUER and AMO_JWT_SECRET it publishes Mozilla's
// signed copy, as an unlisted add-on, which Firefox and LibreWolf both
// install. Without them, or when signing fails, it writes the unsigned
// package, which LibreWolf installs with xpinstall.signatures.required off
// and Firefox does not install. It prints the package's ID, version and
// whether it is signed.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/rimexe0/telemetry/internal/xpi"
)

func main() {
	dir := flag.String("dir", "extensions/telemetry-blocker", "the extension's folder")
	out := flag.String("out", "telemetry-extension.xpi", "where to write the package")
	timeout := flag.Duration("sign-timeout", 15*time.Minute, "how long to wait for Mozilla's signature")
	flag.Parse()
	unsigned, err := xpi.Build(*dir)
	if err != nil {
		log.Fatal(err)
	}
	info, err := xpi.Read(unsigned)
	if err != nil {
		log.Fatal(err)
	}
	result := unsigned
	issuer, secret := os.Getenv("AMO_JWT_ISSUER"), os.Getenv("AMO_JWT_SECRET")
	switch {
	case issuer == "" || secret == "":
		fmt.Fprintln(os.Stderr, "No Mozilla signing credentials: the package is unsigned and installs only in LibreWolf with xpinstall.signatures.required off.")
	default:
		signed, fresh, err := xpi.AMO{Issuer: issuer, Secret: secret, Timeout: *timeout}.Sign(context.Background(), unsigned)
		if err != nil {
			// The rest of the release does not wait on Mozilla; the next one
			// finds this version signed.
			fmt.Printf("::warning::The Firefox package is unsigned this time: %v\n", err)
			break
		}
		if same, err := xpi.SameExtension(unsigned, signed); err != nil || !same {
			fmt.Printf("::warning::Mozilla signed version %s earlier from other files. Give the extension a new version in manifest.json so the change reaches Firefox.\n", info.Version)
		}
		if fresh {
			fmt.Fprintf(os.Stderr, "Mozilla signed version %s.\n", info.Version)
		}
		result = signed
	}
	if err := os.WriteFile(*out, result, 0o644); err != nil {
		log.Fatal(err)
	}
	final, err := xpi.Read(result)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(final.ID, final.Version, map[bool]string{true: "signed", false: "unsigned"}[final.Signed])
}
