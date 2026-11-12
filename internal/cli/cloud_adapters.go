package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"slices"
	"strings"
)

const cloudAdapterHeader = "X-Spine-Cloud-Adapters"
const cloudOfficialAPI = "https://api.spinecloud.dev"

var errCloudAdapterLimit = errors.New("could not report device adapters: more than 96 unique hardware addresses")

func cloudAdapterFingerprints(interfaces []net.Interface) []string {
	keys := make([]string, 0, len(interfaces))
	for _, iface := range interfaces {
		address := iface.HardwareAddr
		if iface.Flags&net.FlagLoopback != 0 || (len(address) != 6 && len(address) != 8) || address[0]&1 != 0 {
			continue
		}
		if !slices.ContainsFunc(address, func(b byte) bool { return b != 0 }) {
			continue
		}
		sum := sha256.Sum256([]byte("spinecloud:adapter:v1:" + address.String()))
		keys = append(keys, hex.EncodeToString(sum[:]))
	}
	slices.Sort(keys)
	return slices.Compact(keys)
}

func cloudAdapterSignal(api string) (string, error) {
	if api != cloudOfficialAPI {
		return "", nil
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		return "", nil
	} // API에서 현재 배포 정책에 따라 누락을 처리한다.
	keys := cloudAdapterFingerprints(interfaces)
	if len(keys) > 96 {
		return "", errCloudAdapterLimit
	}
	return strings.Join(keys, ","), nil
}
