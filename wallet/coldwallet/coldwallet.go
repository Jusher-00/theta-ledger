package coldwallet

import (
	"fmt"
	"strings"
	"sync"

	"github.com/karalabe/hid"

	"github.com/thetatoken/ukulele/common"
	"github.com/thetatoken/ukulele/crypto"
	ks "github.com/thetatoken/ukulele/wallet/coldwallet/keystore"
	"github.com/thetatoken/ukulele/wallet/types"
)

var _ types.Wallet = (*ColdWallet)(nil)

//
// ColdWallet implements the Wallet interface
//

type ColdWallet struct {
	id string

	hub    *Hub // USB hub scanning
	driver ks.Driver

	pathMap map[common.Address]types.DerivationPath // Known derivation paths for signing operations
	info    hid.DeviceInfo                          // Known USB device infos about the wallet
	device  *hid.Device                             // USB device advertising itself as a hardware wallet

	stateLock *sync.RWMutex // Protects read and write access to the wallet struct fields
}

func NewColdWallet(hub *Hub, scheme string, path string) (*ColdWallet, error) {
	var driver ks.Driver
	var err error
	if scheme == LedgerScheme {
		driver = ks.NewLedgerDriver()
	} else {
		panic(fmt.Sprintf("Unsupported cold wallet driver scheme: %v", LedgerScheme))
	}
	if err != nil {
		return nil, err
	}

	walletID := assembleColdWalletID(scheme, path)
	wallet := &ColdWallet{
		id:        walletID,
		hub:       hub,
		driver:    driver,
		pathMap:   nil, // need to set to nil initially
		device:    nil,
		stateLock: &sync.RWMutex{},
	}

	return wallet, nil
}

func (w *ColdWallet) ID() string {
	return w.id
}

func (w *ColdWallet) Status() (string, error) {
	w.stateLock.RLock() // No device communication, state lock is enough
	defer w.stateLock.RUnlock()

	status, failure := w.driver.Status()
	if w.device == nil {
		return "Closed", failure
	}
	return status, failure
}

func (w *ColdWallet) List() ([]common.Address, error) {
	return nil, fmt.Errorf("Not supported for cold wallet")
}

func (w *ColdWallet) NewKey(password string) (common.Address, error) {
	return common.Address{}, fmt.Errorf("Not supported for cold wallet")
}

func (w *ColdWallet) Unlock(address common.Address, password string) error {
	w.stateLock.Lock() // State lock is enough since there's no connection yet at this point
	defer w.stateLock.Unlock()

	if w.pathMap != nil {
		return fmt.Errorf("Wallet already unlocked")
	}
	if w.device == nil {
		device, err := w.info.Open()
		if err != nil {
			return err
		}
		w.device = device
	}

	if err := w.driver.Open(w.device, password); err != nil {
		return err
	}
	w.pathMap = make(map[common.Address]types.DerivationPath)

	return nil
}

func (w *ColdWallet) Lock(address common.Address) error {
	err := w.close()
	return err
}

func (w *ColdWallet) Delete(address common.Address, password string) error {
	return fmt.Errorf("Not supported for cold wallet")
}

func (w *ColdWallet) UpdatePassword(address common.Address, oldPassword, newPassword string) error {
	return fmt.Errorf("Not supported for cold wallet")
}

func (w *ColdWallet) Derive(path types.DerivationPath, pin bool) (common.Address, error) {
	address, err := w.driver.Derive(path)
	return address, err
}

func (w *ColdWallet) GetPublicKey(address common.Address) (*crypto.PublicKey, error) {
	return nil, fmt.Errorf("Not supported for cold wallet")
}

func (w *ColdWallet) Sign(address common.Address, txrlp common.Bytes) (*crypto.Signature, error) {
	w.stateLock.RLock()
	defer w.stateLock.RUnlock()

	if w.device == nil {
		return nil, fmt.Errorf("wallet closed")
	}
	path, ok := w.pathMap[address]
	if !ok {
		return nil, fmt.Errorf("unknown account")
	}

	w.hub.commsLock.Lock()
	w.hub.commsPend++
	w.hub.commsLock.Unlock()

	defer func() {
		w.hub.commsLock.Lock()
		w.hub.commsPend--
		w.hub.commsLock.Unlock()
	}()

	// Sign the transaction and verify the sender to avoid hardware fault surprises
	senderAddr, signed, err := w.driver.SignTx(path, txrlp)
	if err != nil {
		return nil, err
	}
	if senderAddr != address {
		return nil, fmt.Errorf("signer mismatch: expected %s, got %s", address.Hex(), senderAddr.Hex())
	}
	return signed, nil
}

func (w *ColdWallet) setDriver(driver ks.Driver) {
	w.driver = driver
}

func (w *ColdWallet) close() error {
	// Allow duplicate closes, especially for health-check failures
	if w.device == nil {
		return nil
	}
	// Close the device, clear everything, then return
	w.device.Close()
	w.device = nil

	w.pathMap = nil
	w.driver.Close()

	return nil
}

func assembleColdWalletID(scheme, path string) string {
	walletID := "coldwallet:" + scheme + ":" + path
	return walletID
}

func compareColdWalletID(id1, id2 string) int {
	return strings.Compare(id1, id2)
}
