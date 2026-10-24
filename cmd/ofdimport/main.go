// Commande ofdimport : convertit l'export all.json(.gz) de l'Open Filament Database en index de
// référence embarqué (internal/reference/ofd.json). Utilisée par la GitHub Action de mise à jour.
//
//	go run ./cmd/ofdimport -in all.json.gz -out internal/reference/ofd.json
package main

import (
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/MaximeBranger/filament_manager/internal/reference"
)

func main() {
	in := flag.String("in", "all.json.gz", "export all.json ou all.json.gz de l'OFD")
	out := flag.String("out", "internal/reference/ofd.json", "fichier de référence à écrire")
	flag.Parse()
	if err := run(*in, *out); err != nil {
		fmt.Fprintln(os.Stderr, "erreur :", err)
		os.Exit(1)
	}
}

func run(in, out string) error {
	f, err := os.Open(in)
	if err != nil {
		return err
	}
	defer f.Close()
	var r io.Reader = f
	if strings.HasSuffix(in, ".gz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		defer gz.Close()
		r = gz
	}
	data, count, err := reference.Convert(r)
	if err != nil {
		return err
	}
	if count < 1000 {
		return fmt.Errorf("seulement %d codes-barres : export suspect, index non remplacé", count)
	}
	tmp := out + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, out); err != nil {
		return err
	}
	fmt.Printf("%d codes-barres écrits dans %s\n", count, out)
	return nil
}
