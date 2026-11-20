//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

var (
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	user32   = syscall.NewLazyDLL("user32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	advapi32 = syscall.NewLazyDLL("advapi32.dll")
	setupapi = syscall.NewLazyDLL("setupapi.dll")
	hiddll   = syscall.NewLazyDLL("hid.dll")
	shcore   = syscall.NewLazyDLL("shcore.dll")

	pUpdateLayeredWindow = user32.NewProc("UpdateLayeredWindow")
	pShowWindow          = user32.NewProc("ShowWindow")
	pSetTimer            = user32.NewProc("SetTimer")
	pKillTimer           = user32.NewProc("KillTimer")
	pMonitorFromPoint    = user32.NewProc("MonitorFromPoint")
	pGetMonitorInfoW     = user32.NewProc("GetMonitorInfoW")
	pGetDC               = user32.NewProc("GetDC")
	pReleaseDC           = user32.NewProc("ReleaseDC")
	pLoadCursorW         = user32.NewProc("LoadCursorW")
	pLoadIconW           = user32.NewProc("LoadIconW")
	pGetDpiForMonitor    = shcore.NewProc("GetDpiForMonitor")

	pCreateEventW        = kernel32.NewProc("CreateEventW")
	pWaitForSingleObject = kernel32.NewProc("WaitForSingleObject")
	pGetOverlappedResult = kernel32.NewProc("GetOverlappedResult")
	pCancelIoEx          = kernel32.NewProc("CancelIoEx")
	pCreateMutexW        = kernel32.NewProc("CreateMutexW")
	pGetModuleHandleW    = kernel32.NewProc("GetModuleHandleW")

	pRegisterClassExW              = user32.NewProc("RegisterClassExW")
	pCreateWindowExW               = user32.NewProc("CreateWindowExW")
	pDefWindowProcW                = user32.NewProc("DefWindowProcW")
	pDestroyWindow                 = user32.NewProc("DestroyWindow")
	pGetMessageW                   = user32.NewProc("GetMessageW")
	pTranslateMessage              = user32.NewProc("TranslateMessage")
	pDispatchMessageW              = user32.NewProc("DispatchMessageW")
	pPostMessageW                  = user32.NewProc("PostMessageW")
	pPostQuitMessage               = user32.NewProc("PostQuitMessage")
	pCreatePopupMenu               = user32.NewProc("CreatePopupMenu")
	pAppendMenuW                   = user32.NewProc("AppendMenuW")
	pTrackPopupMenu                = user32.NewProc("TrackPopupMenu")
	pDestroyMenu                   = user32.NewProc("DestroyMenu")
	pSetForegroundWindow           = user32.NewProc("SetForegroundWindow")
	pGetCursorPos                  = user32.NewProc("GetCursorPos")
	pRegisterWindowMessageW        = user32.NewProc("RegisterWindowMessageW")
	pCreateIconIndirect            = user32.NewProc("CreateIconIndirect")
	pDestroyIcon                   = user32.NewProc("DestroyIcon")
	pGetSystemMetrics              = user32.NewProc("GetSystemMetrics")
	pSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
	pSetProcessDPIAware            = user32.NewProc("SetProcessDPIAware")

	pCreateCompatibleDC    = gdi32.NewProc("CreateCompatibleDC")
	pCreateDIBSection      = gdi32.NewProc("CreateDIBSection")
	pCreateBitmap          = gdi32.NewProc("CreateBitmap")
	pSelectObject          = gdi32.NewProc("SelectObject")
	pDeleteObject          = gdi32.NewProc("DeleteObject")
	pDeleteDC              = gdi32.NewProc("DeleteDC")
	pCreateFontW           = gdi32.NewProc("CreateFontW")
	pSetTextColor          = gdi32.NewProc("SetTextColor")
	pSetBkMode             = gdi32.NewProc("SetBkMode")
	pGetTextExtentPoint32W = gdi32.NewProc("GetTextExtentPoint32W")
	pTextOutW              = gdi32.NewProc("TextOutW")
	pSetTextCharacterExtra = gdi32.NewProc("SetTextCharacterExtra")
	pGdiFlush              = gdi32.NewProc("GdiFlush")

	pShellNotifyIconW = shell32.NewProc("Shell_NotifyIconW")

	pRegOpenKeyExW    = advapi32.NewProc("RegOpenKeyExW")
	pRegSetValueExW   = advapi32.NewProc("RegSetValueExW")
	pRegQueryValueExW = advapi32.NewProc("RegQueryValueExW")
	pRegDeleteValueW  = advapi32.NewProc("RegDeleteValueW")
	pRegCloseKey      = advapi32.NewProc("RegCloseKey")
	pRegCreateKeyExW  = advapi32.NewProc("RegCreateKeyExW")
	pMessageBoxW      = user32.NewProc("MessageBoxW")
	pFindWindowW      = user32.NewProc("FindWindowW")

	pSetupDiGetClassDevsW             = setupapi.NewProc("SetupDiGetClassDevsW")
	pSetupDiEnumDeviceInterfaces      = setupapi.NewProc("SetupDiEnumDeviceInterfaces")
	pSetupDiGetDeviceInterfaceDetailW = setupapi.NewProc("SetupDiGetDeviceInterfaceDetailW")
	pSetupDiDestroyDeviceInfoList     = setupapi.NewProc("SetupDiDestroyDeviceInfoList")

	pHidD_GetHidGuid        = hiddll.NewProc("HidD_GetHidGuid")
	pHidD_GetAttributes     = hiddll.NewProc("HidD_GetAttributes")
	pHidD_GetPreparsedData  = hiddll.NewProc("HidD_GetPreparsedData")
	pHidD_FreePreparsedData = hiddll.NewProc("HidD_FreePreparsedData")
	pHidP_GetCaps           = hiddll.NewProc("HidP_GetCaps")
	pHidD_SetFeature        = hiddll.NewProc("HidD_SetFeature")
	pHidD_GetInputReport    = hiddll.NewProc("HidD_GetInputReport")
	pHidD_FlushQueue        = hiddll.NewProc("HidD_FlushQueue")
)

const (
	WM_DESTROY     = 0x0002
	WM_COMMAND     = 0x0111
	WM_LBUTTONUP   = 0x0202
	WM_RBUTTONUP   = 0x0205
	WM_CONTEXTMENU = 0x007B
	WM_APP         = 0x8000

	NIM_ADD        = 0
	NIM_MODIFY     = 1
	NIM_DELETE     = 2
	NIF_MESSAGE    = 0x01
	NIF_ICON       = 0x02
	NIF_TIP        = 0x04
	NIF_INFO       = 0x10
	NIF_SHOWTIP    = 0x80
	NIM_SETVERSION = 4
	NIIF_WARNING   = 0x02

	MF_STRING    = 0x0000
	MF_GRAYED    = 0x0001
	MF_CHECKED   = 0x0008
	MF_SEPARATOR = 0x0800

	TPM_RIGHTBUTTON = 0x0002
	TPM_RETURNCMD   = 0x0100
	TPM_NONOTIFY    = 0x0080

	SM_CXSMICON = 49

	HKEY_CURRENT_USER = 0x80000001
	KEY_READ          = 0x20019
	KEY_SET_VALUE     = 0x0002
	REG_SZ            = 1

	DIGCF_PRESENT         = 0x02
	DIGCF_DEVICEINTERFACE = 0x10

	HIDP_STATUS_SUCCESS = 0x00110000

	WAIT_OBJECT_0 = 0
)

type GUID struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

type WNDCLASSEXW struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     uintptr
	HIcon         uintptr
	HCursor       uintptr
	HbrBackground uintptr
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       uintptr
}

type MSG struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      POINT
	_       uint32
}

type POINT struct{ X, Y int32 }

type NOTIFYICONDATAW struct {
	CbSize           uint32
	HWnd             uintptr
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            uintptr
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UVersion         uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         GUID
	HBalloonIcon     uintptr
}

type BITMAPINFOHEADER struct {
	BiSize          uint32
	BiWidth         int32
	BiHeight        int32
	BiPlanes        uint16
	BiBitCount      uint16
	BiCompression   uint32
	BiSizeImage     uint32
	BiXPelsPerMeter int32
	BiYPelsPerMeter int32
	BiClrUsed       uint32
	BiClrImportant  uint32
}

type ICONINFO struct {
	FIcon    int32
	XHotspot uint32
	YHotspot uint32
	HbmMask  uintptr
	HbmColor uintptr
}

type SP_DEVICE_INTERFACE_DATA struct {
	CbSize             uint32
	InterfaceClassGuid GUID
	Flags              uint32
	Reserved           uintptr
}

type HIDD_ATTRIBUTES struct {
	Size          uint32
	VendorID      uint16
	ProductID     uint16
	VersionNumber uint16
}

type HIDP_CAPS struct {
	Usage                   uint16
	UsagePage               uint16
	InputReportByteLength   uint16
	OutputReportByteLength  uint16
	FeatureReportByteLength uint16
	Reserved                [17]uint16
	Rest                    [10]uint16
}

type RECT struct{ Left, Top, Right, Bottom int32 }

type MONITORINFO struct {
	CbSize    uint32
	RcMonitor RECT
	RcWork    RECT
	DwFlags   uint32
}

type SIZE struct{ CX, CY int32 }

func u16(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

func copyU16(dst []uint16, s string) {
	src := syscall.StringToUTF16(s)
	if len(src) > len(dst) {
		src = src[:len(dst)]
		src[len(src)-1] = 0
	}
	copy(dst, src)
}

func ptr[T any](v *T) uintptr { return uintptr(unsafe.Pointer(v)) }
