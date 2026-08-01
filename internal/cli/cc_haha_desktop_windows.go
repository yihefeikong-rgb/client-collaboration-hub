//go:build windows

package cli

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	ccHahahaMaxAncestorDepth      = 64
	ccHahahaTcpTableOwnerPidAll   = 5
	ccHahahaAfInet                = 2
	ccHahahaInitialTcpTableSize   = 64 << 10
	ccHahahaMaxTcpTableSize       = 16 << 20
	ccHahahaErrorInsufficientBuf  = 122
	ccHahahaLoopbackIPv4          = 0x0100007f
	ccHahahaAnyIPv4               = 0x00000000
	ccHahahaProcessQueryLimited   = 0x1000
	ccHahahaTh32csSnapprocess     = 0x00000002
)

type ccHahahaMibTcpRowOwnerPid struct {
	State      uint32
	LocalAddr  uint32
	LocalPort  uint32
	RemoteAddr uint32
	RemotePort uint32
	OwningPid  uint32
}

// verifyCCHahaDesktopBridge binds bridge discovery to the real desktop
// process tree and the loopback listener owned by the bridge sidecar. A
// forged discovery file therefore cannot redirect turns to another local
// service.
func verifyCCHahaDesktopBridge(discovery ccHahahaDesktopBridgeDiscovery) error {
	if discovery.PID <= 0 || discovery.BridgePID <= 0 {
		return errors.New("CC-HAHA desktop bridge identity is invalid")
	}
	if discovery.PID == discovery.BridgePID {
		return errors.New("CC-HAHA desktop owner and bridge sidecar must be different processes")
	}
	if err := verifyCCHahaDesktopProcessExists(discovery.PID); err != nil {
		return fmt.Errorf("CC-HAHA desktop owner process is unavailable: %w", err)
	}
	if err := verifyCCHahaDesktopProcessExists(discovery.BridgePID); err != nil {
		return fmt.Errorf("CC-HAHA bridge sidecar process is unavailable: %w", err)
	}
	if err := verifyCCHahaDesktopDescendant(discovery.PID, discovery.BridgePID); err != nil {
		return err
	}
	port, err := ccHahahaDesktopEndpointPort(discovery.Endpoint)
	if err != nil {
		return err
	}
	if err := verifyCCHahaDesktopPortOwner(uint16(port), discovery.BridgePID); err != nil {
		return err
	}
	return nil
}

func verifyCCHahaDesktopProcessExists(pid int) error {
	handle, err := windows.OpenProcess(ccHahahaProcessQueryLimited, false, uint32(pid))
	if err != nil {
		return err
	}
	windows.CloseHandle(handle)
	return nil
}

// verifyCCHahaDesktopDescendant walks the process parent chain from the
// bridge sidecar upward and requires the desktop owner PID to be an ancestor.
func verifyCCHahaDesktopDescendant(desktopPid, bridgePid int) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(ccHahahaTh32csSnapprocess, 0)
	if err != nil {
		return fmt.Errorf("enumerate CC-HAHA processes: %w", err)
	}
	defer windows.CloseHandle(snapshot)

	parents := make(map[uint32]uint32)
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return fmt.Errorf("read CC-HAHA process list: %w", err)
	}
	for {
		parents[entry.ProcessID] = entry.ParentProcessID
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			if errors.Is(err, syscall.ERROR_NO_MORE_FILES) {
				break
			}
			return fmt.Errorf("read CC-HAHA process list: %w", err)
		}
	}

	current := uint32(bridgePid)
	for depth := 0; depth < ccHahahaMaxAncestorDepth; depth++ {
		if current == uint32(desktopPid) {
			return nil
		}
		parent, ok := parents[current]
		if !ok || parent == 0 || parent == current {
			break
		}
		current = parent
	}
	return errors.New("CC-HAHA bridge sidecar is not a descendant of the desktop owner process")
}

// verifyCCHahaDesktopPortOwner checks that the loopback TCP port from the
// discovery endpoint is currently owned by the bridge sidecar PID.
func verifyCCHahaDesktopPortOwner(port uint16, bridgePid int) error {
	table, err := ccHahahaTcpTableOwnerPid()
	if err != nil {
		return fmt.Errorf("read loopback TCP table: %w", err)
	}
	rows := unsafe.Slice((*ccHahahaMibTcpRowOwnerPid)(unsafe.Pointer(uintptr(unsafe.Pointer(&table.Table[0])))), int(table.NumEntries))
	for _, row := range rows {
		if row.LocalAddr != ccHahahaLoopbackIPv4 && row.LocalAddr != ccHahahaAnyIPv4 {
			continue
		}
		if ccHahahaNtohs(row.LocalPort) != port {
			continue
		}
		if row.OwningPid == uint32(bridgePid) {
			return nil
		}
		return errors.New("CC-HAHA bridge endpoint port is owned by a different process")
	}
	return errors.New("CC-HAHA bridge endpoint port is not listening on loopback")
}

func ccHahahaTcpTableOwnerPid() (*ccHahahaMibTcpTableOwnerPid, error) {
	proc := syscall.NewLazyDLL("iphlpapi.dll").NewProc("GetExtendedTcpTable")
	size := uint32(ccHahahaInitialTcpTableSize)
	for {
		buffer := make([]byte, size)
		result, _, _ := proc.Call(
			uintptr(unsafe.Pointer(&buffer[0])),
			uintptr(unsafe.Pointer(&size)),
			0,
			ccHahahaAfInet,
			ccHahahaTcpTableOwnerPidAll,
			0,
		)
		switch result {
		case 0:
			return (*ccHahahaMibTcpTableOwnerPid)(unsafe.Pointer(&buffer[0])), nil
		case ccHahahaErrorInsufficientBuf:
			if size > ccHahahaMaxTcpTableSize {
				return nil, errors.New("TCP table is too large")
			}
			continue
		default:
			return nil, syscall.Errno(result)
		}
	}
}

func ccHahahaNtohs(value uint32) uint16 {
	return uint16((value & 0xff)<<8 | (value>>8)&0xff)
}

type ccHahahaMibTcpTableOwnerPid struct {
	NumEntries uint32
	Table      [1]ccHahahaMibTcpRowOwnerPid
}
