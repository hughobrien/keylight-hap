package bridge

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"

	"github.com/brutella/hap"
)

const pinFile = "pin.txt"

// loadOrGeneratePin returns the persisted 8-digit HomeKit PIN from
// stateDir/pin.txt, generating and saving a strong one if absent or invalid.
func loadOrGeneratePin(stateDir string) (string, error) {
	path := filepath.Join(stateDir, pinFile)
	if data, err := os.ReadFile(path); err == nil {
		pin := strings.TrimSpace(string(data))
		if validPin(pin) {
			return pin, nil
		}
	}
	pin, err := generatePin()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(pin+"\n"), 0o600); err != nil {
		return "", err
	}
	return pin, nil
}

func validPin(pin string) bool {
	if len(pin) != 8 {
		return false
	}
	for _, r := range pin {
		if r < '0' || r > '9' {
			return false
		}
	}
	_, weak := hap.InvalidPins[pin]
	return !weak
}

func generatePin() (string, error) {
	for {
		var sb strings.Builder
		for i := 0; i < 8; i++ {
			n, err := rand.Int(rand.Reader, big.NewInt(10))
			if err != nil {
				return "", err
			}
			sb.WriteByte(byte('0' + n.Int64()))
		}
		pin := sb.String()
		if _, weak := hap.InvalidPins[pin]; !weak {
			return pin, nil
		}
	}
}

// formatPinDisplay renders an 8-digit pin as "XXXX-XXXX" for logging.
func formatPinDisplay(pin string) string {
	if len(pin) != 8 {
		return pin
	}
	return fmt.Sprintf("%s-%s", pin[:4], pin[4:])
}
