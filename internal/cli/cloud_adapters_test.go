package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"slices"
	"strings"
	"testing"
)

func TestCloudAdapterFingerprints(t *testing.T) {
	eth := net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	wifi := net.HardwareAddr{0x02, 0x10, 0x20, 0x30, 0x40, 0x50}
	got := cloudAdapterFingerprints([]net.Interface{
		{Flags: net.FlagUp, HardwareAddr: eth}, {HardwareAddr: wifi}, {HardwareAddr: eth},
		{Flags: net.FlagLoopback, HardwareAddr: net.HardwareAddr{0, 1, 2, 3, 4, 6}},
		{HardwareAddr: net.HardwareAddr{0, 0, 0, 0, 0, 0}},
		{HardwareAddr: net.HardwareAddr{1, 1, 1, 1, 1, 1}},
	})
	var want []string
	for _, a := range []net.HardwareAddr{eth, wifi} {
		sum := sha256.Sum256([]byte("spinecloud:adapter:v1:" + a.String()))
		want = append(want, hex.EncodeToString(sum[:]))
	}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("unexpected fingerprints: %v", got)
	}
	if strings.Contains(strings.Join(got, ","), eth.String()) {
		t.Fatal("raw MAC address leaked")
	}
	if signal, err := cloudAdapterSignal("https://api.example.com"); err != nil || signal != "" {
		t.Fatal("third party API received adapter signal")
	}
}
