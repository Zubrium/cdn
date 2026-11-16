package integration

import "encoding/binary"

func sitepackCrossInstallWebP(label string) []byte {
	// Sitepack validates the RIFF/WEBP signature. This deterministic fixture
	// avoids depending on a machine-specific image encoder during import tests.
	data := []byte("RIFF\x00\x00\x00\x00WEBPVP8 " + "\x04\x00\x00\x00" + "\x00\x00\x00\x00" + label)
	binary.LittleEndian.PutUint32(data[4:8], uint32(len(data)-8))
	return data
}
