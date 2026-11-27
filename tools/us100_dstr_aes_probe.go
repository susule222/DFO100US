// Offline known-text probe for the local US100 localization asset. It never writes files.
package main

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"debug/pe"
	"encoding/binary"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"unicode/utf16"
)

func main() {
	image := flag.String("exe", "", "local US100 executable owning the asset")
	ciphertext := flag.String("dstr", "", "original 100US dstr.dat")
	plain := flag.String("table", "", "matching original UTF-16LE dnf.uv.str")
	stride := flag.Int("stride", 4, "candidate alignment in PE section bytes")
	sections := flag.String("sections", ".rdata,.data,_RDATA", "comma-separated PE sections")
	flag.Parse()
	if *stride < 1 || *stride > 16 || *image == "" || *ciphertext == "" || *plain == "" {
		flag.Usage()
		os.Exit(2)
	}
	c, err := os.ReadFile(*ciphertext)
	if err != nil {
		log.Fatal(err)
	}
	w, err := os.ReadFile(*plain)
	if err != nil {
		log.Fatal(err)
	}
	if len(w) < 256 || len(w)%2 != 0 || len(c) < 128 || len(c)%aes.BlockSize != 0 {
		log.Fatal("unexpected source lengths")
	}
	w = bytes.TrimPrefix(w, []byte{0xff, 0xfe})
	chars := make([]uint16, len(w)/2)
	for i := range chars {
		chars[i] = binary.LittleEndian.Uint16(w[i*2:])
	}
	p := []byte(string(utf16.Decode(chars)))
	if len(p)/16*16+16 != len(c) {
		log.Fatal("source lengths do not match the observed padding hypothesis")
	}

	f, err := pe.Open(*image)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	type signature struct {
		label string
		text  []byte
		value [16]byte
		cbc   bool
	}
	var known []signature
	variants := []struct {
		label string
		text  []byte
	}{
		{"original", p},
		{"UTF-8 BOM", append([]byte{0xef, 0xbb, 0xbf}, p...)},
		{"without initial CRLF", bytes.TrimPrefix(p, []byte("\r\n"))},
	}
	for _, v := range variants {
		var ecb, cbc [16]byte
		copy(ecb[:], v.text[16:32])
		for i := range cbc {
			cbc[i] = ecb[i] ^ c[i]
		}
		known = append(known, signature{v.label, v.text, cbc, true}, signature{v.label, v.text, ecb, false})
	}
	var got [16]byte
	total := 0
	for _, name := range strings.Split(*sections, ",") {
		s := f.Section(name)
		if s == nil {
			log.Fatalf("missing section %s", name)
		}
		raw, err := s.Data()
		if err != nil {
			log.Fatal(err)
		}
		before := total
		for off := 0; off+16 <= len(raw); off += *stride {
			for _, length := range [...]int{32, 16, 24} {
				if off+length > len(raw) {
					continue
				}
				total++
				block, _ := aes.NewCipher(raw[off : off+length])
				block.Decrypt(got[:], c[16:32])
				for _, signature := range known {
					if got != signature.value {
						continue
					}
					out := make([]byte, len(c))
					if signature.cbc {
						var iv [16]byte
						block.Decrypt(iv[:], c[:16])
						for i := range iv {
							iv[i] ^= signature.text[i]
						}
						cipher.NewCBCDecrypter(block, iv[:]).CryptBlocks(out, c)
					} else {
						for base := 0; base < len(c); base += 16 {
							block.Decrypt(out[base:base+16], c[base:base+16])
						}
					}
					if bytes.HasPrefix(out, signature.text) {
						fmt.Printf("matching full known text: section=%s rva=%#x key_bits=%d cbc=%t variant=%s\n",
							name, s.VirtualAddress+uint32(off), length*8, signature.cbc, signature.label)
						return
					}
				}
			}
		}
		fmt.Printf("%s: %d candidate keys, no match\n", name, total-before)
	}
	fmt.Printf("No AES-CBC or AES-ECB match across %d candidates\n", total)
}
