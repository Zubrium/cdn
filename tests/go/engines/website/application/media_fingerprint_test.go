package application

import "testing"

func TestMediaRequestFingerprintPreservesExistingInputIdentity(t *testing.T) {
	values := []string{"source", "checksum", "snapshot", "placement", "/hero/image", "profile", "processor"}
	previous, err := mediaFingerprint(values...)
	if err != nil {
		t.Fatal(err)
	}
	current, err := mediaRequestFingerprint("dataInputs", values...)
	if err != nil {
		t.Fatal(err)
	}
	if current != previous {
		t.Fatal("existing dataInputs receipts would no longer be found")
	}
	argument, err := mediaRequestFingerprint("arguments", values...)
	if err != nil {
		t.Fatal(err)
	}
	if argument == current {
		t.Fatal("argument and data input identities must not collide")
	}
	created, err := mediaFingerprint("source", "checksum", "snapshot", "placement", "arguments", "/hero/image", "profile", "processor")
	if err != nil || argument != created {
		t.Fatal("existing argument receipt identity must remain unchanged")
	}
	if _, err := mediaRequestFingerprint("unknown", values...); err == nil {
		t.Fatal("unknown schema area must be rejected")
	}
}
