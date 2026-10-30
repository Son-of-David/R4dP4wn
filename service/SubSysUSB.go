// +build linux

package service

import (
	"context"
	"errors"
	"fmt"
	"github.com/mame82/P4wnP1_aloa/common"
	"github.com/mame82/P4wnP1_aloa/hid"
	pb "github.com/mame82/P4wnP1_aloa/proto"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	USB_EP_USAGE_HID_RAW = 1
	USB_EP_USAGE_HID_KEYBOARD = 1
	USB_EP_USAGE_HID_MOUSE = 1
	USB_EP_USAGE_RNDIS = 2
	USB_EP_USAGE_CDC_ECM = 2
	USB_EP_USAGE_CDC_SERIAL = 2
	USB_EP_USAGE_UMS = 2
	USB_EP_USAGE_MAX = 7
	USB_EP_USAGE_MAX_DWC3 = 16

	USB_GADGET_NAME = "mame82_gadget"
	USB_GADGET_DIR_BASE      = "/sys/kernel/config/usb_gadget"
	USB_GADGET_DIR           = USB_GADGET_DIR_BASE + "/" + USB_GADGET_NAME

	USB_bcdDevice = "0x0101" //Version 1.01 (Windows re-reads OS descriptors)
	USB_bcdUSB    = "0x0200" //mode: USB 2.0

	// composite class / subclass / proto (needs single configuration)
	USB_bDeviceClass    = "0xEF"
	USB_bDeviceSubClass = "0x02"
	USB_bDeviceProtocol = "0x01"

	USB_CONFIGURATION_MaxPower     = "250"
	USB_CONFIGURATION_bmAttributes = "0x80" //should be 0x03 for USB_OTG_SRP | USB_OTG_HNP

	//OS descriptors for RNDIS composite function on Windows
	USB_FUNCTION_RNDIS_os_desc_use                         = "1"
	USB_FUNCTION_RNDIS_os_desc_b_vendor_code               = "0xbc"
	USB_FUNCTION_RNDIS_os_desc_qw_sign                     = "MSFT100"
	USB_FUNCTION_RNDIS_os_desc_interface_compatible_id     = "RNDIS"
	USB_FUNCTION_RNDIS_os_desc_interface_sub_compatible_id = "5162001"

	//HID function, keyboard constants
	USB_FUNCTION_HID_KEYBOARD_protocol      = "1"
	USB_FUNCTION_HID_KEYBOARD_subclass      = "1"
	USB_FUNCTION_HID_KEYBOARD_report_length = "8"
	USB_FUNCTION_HID_KEYBOARD_report_desc   = "\x05\x01\t\x06\xa1\x01\x05\x07\x19\xe0)\xe7\x15\x00%\x01u\x01\x95\x08\x81\x02\x95\x01u\x08\x81\x03\x95\x05u\x01\x05\x08\x19\x01)\x05\x91\x02\x95\x01u\x03\x91\x03\x95\x06u\x08\x15\x00%e\x05\x07\x19\x00)e\x81\x00\xc0"
	USB_FUNCTION_HID_KEYBOARD_name          = "hid.keyboard"

	//HID function, mouse constants
	USB_FUNCTION_HID_MOUSE_protocol      = "2"
	USB_FUNCTION_HID_MOUSE_subclass      = "1"
	USB_FUNCTION_HID_MOUSE_report_length = "6"
	USB_FUNCTION_HID_MOUSE_report_desc   = "\x05\x01\t\x02\xa1\x01\t\x01\xa1\x00\x85\x01\x05\t\x19\x01)\x03\x15\x00%\x01\x95\x03u\x01\x81\x02\x95\x01u\x05\x81\x03\x05\x01\t0\t1\x15\x81%\x7fu\x08\x95\x02\x81\x06\x95\x02u\x08\x81\x01\xc0\xc0\x05\x01\t\x02\xa1\x01\t\x01\xa1\x00\x85\x02\x05\t\x19\x01)\x03\x15\x00%\x01\x95\x03u\x01\x81\x02\x95\x01u\x05\x81\x01\x05\x01\t0\t1\x15\x00&\xff\x7f\x95\x02u\x10\x81\x02\xc0\xc0"
	USB_FUNCTION_HID_MOUSE_name          = "hid.mouse"

	//HID function, custom vendor device constants
	USB_FUNCTION_HID_RAW_protocol      = "1"
	USB_FUNCTION_HID_RAW_subclass      = "1"
	USB_FUNCTION_HID_RAW_report_length = "64"
	USB_FUNCTION_HID_RAW_report_desc   = "\x06\x00\xff\t\x01\xa1\x01\t\x01\x15\x00&\xff\x00u\x08\x95@\x81\x02\t\x02\x15\x00&\xff\x00u\x08\x95@\x91\x02\xc0"
	USB_FUNCTION_HID_RAW_name          = "hid.raw"



)

var (
	ErrUsbNotUsable = errors.New("USB subsystem not available")
	ErrHidNotUsable = errors.New("HIDScript not available (mouse and keyboard disabled)")
	rp_usbHidDevName                      = regexp.MustCompile("(?m)DEVNAME=(.*)\n")
)

type UsbManagerState struct {
	//UndeployedGadgetSettings *pb.GadgetSettings
	DevicePath map[string]string
}

type UsbGadgetManager struct {
	RootSvc *Service
	Usable bool

	State *UsbManagerState
	// ToDo: variable, indicating if HIDScript is usable
	hidCtl             *hid.HIDController // Points to an HID controller instance only if keyboard and/or mouse are enabled, nil otherwise
	gadgetSettingsLock *sync.Mutex
	udcWatchStop       chan struct{}
}

func (gm *UsbGadgetManager) HandleEvent(event hid.Event) {
	fmt.Printf("GADGET MANAGER HID EVENT: %+v\n", event)
	gm.RootSvc.SubSysEvent.Emit(ConstructEventHID(event))
}

func (gm *UsbGadgetManager) HidScriptUsable() error {
	// Fix: If a TriggerAction fires a HIDScript during Deployment of GadgetSettings, this method is called
	// upfront, to check if mouse or keyboard are up
	// We avoid returning false, if Deployment is still going on (undefined state), so we block
	gm.gadgetSettingsLock.Lock()
	defer gm.gadgetSettingsLock.Unlock()

	if gm.hidCtl == nil {
		if err := gm.initHIDControllerLocked(); err != nil {
			return ErrHidNotUsable
		}
	}
	if gm.hidCtl == nil {
		return ErrHidNotUsable
	}
	return nil
}

func (gm *UsbGadgetManager) HidScriptRun(ctx context.Context, scriptContent string) (result interface{}, err error) {
	err = gm.HidScriptUsable()
	if err != nil {	return }

	scriptVal,err := gm.hidCtl.RunScript(ctx, scriptContent, true)
	if err != nil { return nil, err}

	return scriptVal.Export()
}

func (gm *UsbGadgetManager) HidScriptStartBackground(ctx context.Context, scriptContent string) (job *hid.AsyncOttoJob, err error) {
	err = gm.HidScriptUsable()
	if err != nil {	return }

	return gm.hidCtl.StartScriptAsBackgroundJob(ctx, scriptContent, true)
}

//WaitBackgroundJobResult(ctx context.Context, job *AsyncOttoJob) (val otto.Value, err error) {
func (gm *UsbGadgetManager) HidScriptWaitBackgroundJobResult(ctx context.Context, job *hid.AsyncOttoJob) (result interface{}, err error) {
	err = gm.HidScriptUsable()
	if err != nil {	return }

	scriptVal,err := gm.hidCtl.WaitBackgroundJobResult(ctx, job)
	if err != nil { return nil, err}

	return scriptVal.Export()
}

func (gm *UsbGadgetManager) HidScriptGetBackgroundJobByID(id int) (job *hid.AsyncOttoJob, err error) {
	err = gm.HidScriptUsable()
	if err != nil {	return }

	return gm.hidCtl.GetBackgroundJobByID(id)
}

func (gm *UsbGadgetManager) HidScriptGetAllRunningBackgroundJobs() (jobs []*hid.AsyncOttoJob, err error) {
	err = gm.HidScriptUsable()
	if err != nil {	return }

	return gm.hidCtl.GetAllBackgroundJobs()
}

func (gm *UsbGadgetManager) HidScriptCancelAllRunningBackgroundJobs() (err error) {
	err = gm.HidScriptUsable()
	if err != nil {	return }

	gm.hidCtl.CancelAllBackgroundJobs()
	return
}



func NewUSBGadgetManager(rooSvc *Service) (newUGM *UsbGadgetManager, err error) {
	newUGM = &UsbGadgetManager{
		RootSvc:            rooSvc,
		Usable:             true,
		gadgetSettingsLock: &sync.Mutex{},
		State: &UsbManagerState{
			DevicePath: map[string]string{},
		},
	}

	if err = CheckLibComposite(); err != nil {
		//return nil, errors.New(fmt.Sprintf("Couldn't load libcomposite: %v", err))
		newUGM.Usable = false
		return newUGM,nil
	}



	newUGM.State.DevicePath[USB_FUNCTION_HID_KEYBOARD_name] = ""
	newUGM.State.DevicePath[USB_FUNCTION_HID_MOUSE_name] = ""
	newUGM.State.DevicePath[USB_FUNCTION_HID_RAW_name] = ""


	// Do not deploy here. Startup master template / servicestart.sh deploy once.
	// Deploying now and again ~4s later tears down usb0/usb1 and drops the host.
	log.Println("USB gadget manager ready; startup template will deploy the gadget")
	return
}



func ValidateGadgetSetting(gs *pb.GadgetSettings) error {
	/* ToDo: validations
	- Done: check host_addr/dev_addr of RNDIS + CDC ECM to be valid MAC addresses via regex
	- check host_addr/dev_addr of RNDIS + CDC ECM for duplicates
	- check EP consumption to be not more than 7 (ECM 2 EP, RNDIS 2 EP, HID Mouse 1 EP, HID Keyboard 1 EP, HID Raw 1 EP, Serial 2 EP ??, UMS 2 EP ?)
	- check serial, product, Manufacturer to not be empty
	- check Pid, Vid with regex (Note: we don't check if Vid+Pid have been used for another composite function setup, yet)
	- Done: If the gadget is enabled, at least one function has to be enabled
	 */

	log.Println("Validating gadget settings ...")
	if gs == nil {
		return errors.New("gadget settings are nil")
	}

	if gs.Use_RNDIS {
		if gs.RndisSettings == nil {
			return errors.New("RNDIS enabled but ethernet settings are missing")
		}
		_, err := net.ParseMAC(gs.RndisSettings.DevAddr)
		if err != nil { return errors.New(fmt.Sprintf("Validation Error RNDIS DeviceAddress: %v", err))}

		_, err = net.ParseMAC(gs.RndisSettings.HostAddr)
		if err != nil { return errors.New(fmt.Sprintf("Validation Error RNDIS HostAddress: %v", err))}
	}

	if gs.Use_CDC_ECM {
		if gs.CdcEcmSettings == nil {
			return errors.New("CDC ECM enabled but ethernet settings are missing")
		}
		_, err := net.ParseMAC(gs.CdcEcmSettings.DevAddr)
		if err != nil { return errors.New(fmt.Sprintf("Validation Error CDC ECM DeviceAddress: %v", err))}

		_, err = net.ParseMAC(gs.CdcEcmSettings.HostAddr)
		if err != nil { return errors.New(fmt.Sprintf("Validation Error CDC ECM HostAddress: %v", err))}
	}

	//check endpoint consumption
	sumEp := 0
	if gs.Use_RNDIS { sumEp += USB_EP_USAGE_RNDIS }
	if gs.Use_CDC_ECM { sumEp += USB_EP_USAGE_CDC_ECM }
	if gs.Use_UMS { sumEp += USB_EP_USAGE_UMS }
	if gs.Use_HID_MOUSE { sumEp += USB_EP_USAGE_HID_MOUSE }
	if gs.Use_HID_RAW { sumEp += USB_EP_USAGE_HID_RAW }
	if gs.Use_HID_KEYBOARD { sumEp += USB_EP_USAGE_HID_KEYBOARD }
	if gs.Use_SERIAL { sumEp += USB_EP_USAGE_CDC_SERIAL }

	strConsumption := fmt.Sprintf("Gadget Settings consume %d out of %d available USB Endpoints\n", sumEp, usbEndpointBudget())
	log.Print(strConsumption)
	if sumEp > usbEndpointBudget() { return errors.New(strConsumption)}

	if gs.Enabled &&
		!gs.Use_CDC_ECM &&
		!gs.Use_RNDIS &&
		!gs.Use_HID_KEYBOARD &&
		!gs.Use_HID_MOUSE &&
		!gs.Use_HID_RAW &&
		!gs.Use_UMS &&
		!gs.Use_SERIAL {
			return errors.New("if the composite gadget isn't disabled, as least one function has to be enabled")
	}

	return nil
}

func addUSBEthernetBridge() {
	//Create the bridge
	CreateBridge(USB_ETHERNET_BRIDGE_NAME)
	setInterfaceMac(USB_ETHERNET_BRIDGE_NAME, USB_ETHERNET_BRIDGE_MAC)
	SetBridgeSTP(USB_ETHERNET_BRIDGE_NAME, false) //aboid loosing time by learning interface states, both usb0 and usb1 have to be set to forward
	SetBridgeForwardDelay(USB_ETHERNET_BRIDGE_NAME, 0)

	//add the interfaces
	if err := AddInterfaceToBridgeIfExistent(USB_ETHERNET_BRIDGE_NAME, "usb0"); err != nil {
		log.Println(err)
	}
	if err := AddInterfaceToBridgeIfExistent(USB_ETHERNET_BRIDGE_NAME, "usb1"); err != nil {
		log.Println(err)
	}

	//enable the bridge
	NetworkLinkUp(USB_ETHERNET_BRIDGE_NAME)
}

func deleteUSBEthernetBridge() {
	//we ignore error results
	DeleteBridge(USB_ETHERNET_BRIDGE_NAME)
}

/*
Polls for presence of "usb0" / "usb1" till one of both is active or timeout is reached
 */

func pollForUSBEthernet(timeout time.Duration) error {
	for startTime := time.Now(); time.Since(startTime) < timeout; {
		if present := CheckInterfaceExistence("usb0"); present {
			return nil
		}
		if present := CheckInterfaceExistence("usb1"); present {
			return nil
		}

		//Take a breath
		time.Sleep(100*time.Millisecond)
		fmt.Print(".")
	}
	return errors.New(fmt.Sprintf("timeout %v reached before usb0 or usb1 became ready", timeout))
}




func usbEndpointBudget() int {
	name, err := getUDCName()
	if err != nil {
		return USB_EP_USAGE_MAX
	}
	lname := strings.ToLower(name)
	if strings.Contains(lname, "dwc2") || strings.Contains(lname, "20980000") {
		return USB_EP_USAGE_MAX
	}
	return USB_EP_USAGE_MAX_DWC3
}

func ensureConfigFS() error {
	if _, err := os.Stat(USB_GADGET_DIR_BASE); err == nil {
		return nil
	}
	_ = os.MkdirAll("/sys/kernel/config", 0755)
	if err := exec.Command("mount", "-t", "configfs", "none", "/sys/kernel/config").Run(); err != nil {
		if _, err2 := os.Stat(USB_GADGET_DIR_BASE); err2 == nil {
			return nil
		}
		return err
	}
	return nil
}

func isOTGRolePath(path string) bool {
	l := strings.ToLower(path)
	// Never touch the RK3566 USB3 host controller (fd000000).
	if strings.Contains(l, "fd000000") || strings.Contains(l, "usb_host1") {
		return false
	}
	return strings.Contains(l, "fcc00000") || strings.Contains(l, "fc000000") ||
		strings.Contains(l, "dwc2") || strings.Contains(l, "20980000")
}

func writeUSBRoleFile(path, value string) bool {
	if !isOTGRolePath(path) {
		return false
	}
	type result struct {
		err  error
		prev string
	}
	ch := make(chan result, 1)
	go func() {
		prev, _ := ioutil.ReadFile(path)
		err := ioutil.WriteFile(path, []byte(value+"\n"), 0644)
		ch <- result{err, strings.TrimSpace(string(prev))}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			log.Printf("USB role %s: %v (was %q)", path, r.err, r.prev)
			return false
		}
		log.Printf("USB role %s: %q -> %q", path, r.prev, value)
		return true
	case <-time.After(2 * time.Second):
		log.Printf("USB role %s: timed out writing %q (continuing)", path, value)
		return false
	}
}

func collectOTGRolePaths() []string {
	globs := []string{
		"/sys/kernel/debug/usb/*/mode",
		"/sys/class/usb_role/*/role",
		"/sys/class/udc/*/device/mode",
		"/sys/devices/platform/fcc00000.usb/usb_role/*/role",
		"/sys/devices/platform/fcc00000.dwc3/usb_role/*/role",
		"/sys/bus/platform/devices/fcc00000.usb/usb_role/*/role",
	}
	seen := map[string]bool{}
	var out []string
	for _, g := range globs {
		matches, _ := filepath.Glob(g)
		for _, path := range matches {
			if seen[path] || !isOTGRolePath(path) {
				continue
			}
			seen[path] = true
			out = append(out, path)
		}
	}
	return out
}

func logUDCState(udc string) {
	if udc == "" {
		return
	}
	st := "?"
	if b, err := ioutil.ReadFile("/sys/class/udc/" + udc + "/state"); err == nil {
		st = strings.TrimSpace(string(b))
	}
	mode := "?"
	for _, p := range []string{
		"/sys/kernel/debug/usb/" + strings.TrimSuffix(udc, ".usb") + ".dwc3/mode",
		"/sys/kernel/debug/usb/" + udc + "/mode",
	} {
		if b, err := ioutil.ReadFile(p); err == nil {
			mode = strings.TrimSpace(string(b))
			break
		}
	}
	log.Printf("USB UDC %s state=%s dwc3_mode=%s", udc, st, mode)
}

func ensureDebugFS() {
	if _, err := os.Stat("/sys/kernel/debug/usb"); err == nil {
		return
	}
	_ = os.MkdirAll("/sys/kernel/debug", 0755)
	done := make(chan error, 1)
	go func() {
		done <- exec.Command("mount", "-t", "debugfs", "none", "/sys/kernel/debug").Run()
	}()
	select {
	case err := <-done:
		if err != nil {
			log.Printf("USB role: could not mount debugfs: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		log.Printf("USB role: debugfs mount timed out (continuing)")
	}
}

func setUSBRoleDevice() {
	ensureDebugFS()
	udc, _ := getUDCName()
	log.Printf("USB role: targeting OTG UDC %q only", udc)

	candidates := collectOTGRolePaths()
	if udc != "" && isOTGRolePath(udc) {
		dwc3 := strings.TrimSuffix(udc, ".usb") + ".dwc3"
		for _, path := range []string{
			"/sys/kernel/debug/usb/" + udc + "/mode",
			"/sys/kernel/debug/usb/" + dwc3 + "/mode",
			"/sys/class/udc/" + udc + "/device/mode",
			"/sys/class/usb_role/" + udc + "-role-switch/role",
			"/sys/devices/platform/" + udc + "/usb_role/" + udc + "-role-switch/role",
			"/sys/bus/platform/devices/" + udc + "/usb_role/" + udc + "-role-switch/role",
		} {
			if _, err := os.Stat(path); err == nil {
				found := false
				for _, c := range candidates {
					if c == path {
						found = true
						break
					}
				}
				if !found {
					candidates = append(candidates, path)
				}
			}
		}
	}

	wrote := 0
	for _, path := range candidates {
		// dwc3 debugfs "mode" and usb_role-switch both take host/device, not "peripheral".
		if writeUSBRoleFile(path, "device") {
			wrote++
		}
	}
	if wrote == 0 {
		log.Printf("USB role: no writable OTG mode/role node (continuing without blocking WiFi)")
	}
	logUDCState(udc)
}

func loadUSBFunctionModules() {
	for _, mod := range []string{
		"libcomposite",
		"usb_f_rndis",
		"usb_f_ecm",
		"usb_f_ncm",
		"usb_f_hid",
		"usb_f_mass_storage",
		"usb_f_acm",
	} {
		if err := exec.Command("modprobe", mod).Run(); err != nil {
			log.Printf("modprobe %s: %v", mod, err)
		}
	}
}

func CheckLibComposite() error {
	log.Println("Checking for libcomposite...")
	if err := ensureConfigFS(); err != nil {
		log.Printf("configfs not available: %v", err)
	}
	loadUSBFunctionModules()

	out, err := exec.Command("lsmod").Output()
	if err != nil {
		log.Printf("lsmod failed: %v", err)
	} else if strings.Contains(string(out), "libcomposite") {
		log.Println("... libcomposite loaded")
		return nil
	}

	if _, err := os.Stat(USB_GADGET_DIR_BASE); err == nil {
		log.Println("... usb gadget configfs present")
		return nil
	}
	return errors.New("libcomposite / usb gadget configfs not available")
}

func udcPreference(name string) int {
	l := strings.ToLower(name)
	switch {
	case strings.Contains(l, "dummy"):
		return -100
	case strings.Contains(l, "dwc3"):
		return 100
	case strings.Contains(l, "fcc00000"), strings.Contains(l, "fc000000"), strings.Contains(l, "ff400000"):
		return 90
	case strings.Contains(l, "dwc2"), strings.Contains(l, "20980000"):
		// Raspberry Pi UDC, or a leftover dwc2 module on RK3566 that is not the Type-C port.
		return 10
	default:
		return 50
	}
}

func getUDCName() (string, error) {
	files, err := ioutil.ReadDir("/sys/class/udc")
	if err != nil {
		return "", errors.New("couldn't find working UDC driver")
	}
	if len(files) < 1 {
		return "", errors.New("couldn't find working UDC driver")
	}
	best := files[0].Name()
	bestScore := udcPreference(best)
	for _, f := range files {
		name := f.Name()
		if s := udcPreference(name); s > bestScore {
			best = name
			bestScore = s
		}
	}
	return best, nil
}

func logGadgetLinkState() {
	for _, name := range []string{"usb0", "usb1", "usbeth"} {
		oper := "missing"
		if b, err := ioutil.ReadFile("/sys/class/net/" + name + "/operstate"); err == nil {
			oper = strings.TrimSpace(string(b))
		}
		carrier := "?"
		if b, err := ioutil.ReadFile("/sys/class/net/" + name + "/carrier"); err == nil {
			carrier = strings.TrimSpace(string(b))
		}
		log.Printf("USB ethernet %s operstate=%s carrier=%s", name, oper, carrier)
	}
}

func logSelectedUDC() {
	name, err := getUDCName()
	if err != nil {
		log.Printf("Available UDCs: none (%v)", err)
		return
	}
	files, _ := ioutil.ReadDir("/sys/class/udc")
	names := make([]string, 0, len(files))
	for _, f := range files {
		names = append(names, f.Name())
	}
	log.Printf("Available UDCs: %s; selected %s", strings.Join(names, ", "), name)
}

func (gm *UsbGadgetManager) ParseGadgetState(gadgetName string) (result *pb.GadgetSettings, err error) {
	err = nil
	result = &pb.GadgetSettings{}

	fmt.Println("ParseGadgetState before lock ...")
	//Don't parse while deploying
	gm.gadgetSettingsLock.Lock()
	defer gm.gadgetSettingsLock.Unlock()

	fmt.Println("ParseGadgetState beyond lock ...")

	gadgetDir := USB_GADGET_DIR_BASE + "/" + gadgetName

	//check if root exists, return error otherwise
	if _, err = os.Stat(gadgetDir); os.IsNotExist(err) {
		err = errors.New(fmt.Sprintf("gadget %s doesn't exist", gadgetName))
		result = nil
		return
	}

	//ToDo: check if enabled (UDC in functionfs is set to content of /sys/class/udc)

	if res, err := ioutil.ReadFile(gadgetDir + "/idVendor"); err != nil {
		err1 := errors.New(fmt.Sprintf("gadget %s error reading Vid", gadgetName))
		return nil, err1
	} else {
		result.Vid = strings.TrimSuffix(string(res), "\n")
	}

	if res, err := ioutil.ReadFile(gadgetDir + "/idProduct"); err != nil {
		err1 := errors.New(fmt.Sprintf("gadget %s error reading Pid", gadgetName))
		return nil, err1
	} else {
		result.Pid = strings.TrimSuffix(string(res), "\n")
	}

	if res, err := ioutil.ReadFile(gadgetDir + "/strings/0x409/serialnumber"); err != nil {
		err1 := errors.New(fmt.Sprintf("gadget %s error reading Serial", gadgetName))
		return nil, err1
	} else {
		result.Serial = strings.TrimSuffix(string(res), "\n")
	}

	if res, err := ioutil.ReadFile(gadgetDir + "/strings/0x409/manufacturer"); err != nil {
		err1 := errors.New(fmt.Sprintf("gadget %s error reading Manufacturer", gadgetName))
		return nil, err1
	} else {
		result.Manufacturer = strings.TrimSuffix(string(res), "\n")
	}

	if res, err := ioutil.ReadFile(gadgetDir + "/strings/0x409/product"); err != nil {
		err1 := errors.New(fmt.Sprintf("gadget %s error reading Product", gadgetName))
		return nil, err1
	} else {
		result.Product = strings.TrimSuffix(string(res), "\n")
	}

	//Check enabled functions in configuration

	//USB RNDIS
	if _, err1 := os.Stat(gadgetDir +"/configs/c.1/rndis.usb0"); !os.IsNotExist(err1) {
		result.Use_RNDIS = true

		result.RndisSettings = &pb.GadgetSettingsEthernet{}

		if res, err := ioutil.ReadFile(gadgetDir + "/functions/rndis.usb0/host_addr"); err != nil {
			err1 := errors.New(fmt.Sprintf("gadget %s error reading RNDIS host_addr", gadgetName))
			return nil, err1
		} else {
			result.RndisSettings.HostAddr = strings.TrimSuffix(string(res), "\000\n")
		}

		if res, err := ioutil.ReadFile(gadgetDir + "/functions/rndis.usb0/dev_addr"); err != nil {
			err1 := errors.New(fmt.Sprintf("gadget %s error reading RNDIS dev_addr", gadgetName))
			return nil, err1
		} else {
			result.RndisSettings.DevAddr = strings.TrimSuffix(string(res), "\000\n")
		}
	} else {
		// we provide GadgetSettingsEthernet with default MAC adresses anyway, to have defaults in case RNDIS should be enabled
		result.RndisSettings = &pb.GadgetSettingsEthernet{
			HostAddr: DEFAULT_RNDIS_HOST_ADDR,
			DevAddr: DEFAULT_RNDIS_DEV_ADDR,
		}
	}

	//USB CDC ECM or NCM (NCM is what Windows 10/11 actually speaks)
	ethFunc := ""
	if _, err1 := os.Stat(gadgetDir + "/configs/c.1/ncm.usb1"); !os.IsNotExist(err1) {
		ethFunc = "ncm.usb1"
	} else if _, err1 := os.Stat(gadgetDir + "/configs/c.1/ecm.usb1"); !os.IsNotExist(err1) {
		ethFunc = "ecm.usb1"
	}
	if ethFunc != "" {
		result.Use_CDC_ECM = true
		result.CdcEcmSettings = &pb.GadgetSettingsEthernet{}
		if res, err := ioutil.ReadFile(gadgetDir + "/functions/" + ethFunc + "/host_addr"); err != nil {
			return nil, errors.New(fmt.Sprintf("gadget %s error reading %s host_addr", gadgetName, ethFunc))
		} else {
			result.CdcEcmSettings.HostAddr = strings.TrimSuffix(string(res), "\000\n")
		}
		if res, err := ioutil.ReadFile(gadgetDir + "/functions/" + ethFunc + "/dev_addr"); err != nil {
			return nil, errors.New(fmt.Sprintf("gadget %s error reading %s dev_addr", gadgetName, ethFunc))
		} else {
			result.CdcEcmSettings.DevAddr = strings.TrimSuffix(string(res), "\000\n")
		}
	} else {
		// we provide GadgetSettingsEthernet with default MAC adresses anyway, to have defaults in case CDC ECM should be enabled
		result.CdcEcmSettings = &pb.GadgetSettingsEthernet{
			HostAddr: DEFAULT_CDC_ECM_HOST_ADDR,
			DevAddr: DEFAULT_CDC_ECM_DEV_ADDR,
		}
	}

	//USB serial
	if _, err1 := os.Stat(gadgetDir +"/configs/c.1/acm.GS0"); !os.IsNotExist(err1) {
		result.Use_SERIAL = true
	}

	//USB HID Keyboard
	if _, err1 := os.Stat(gadgetDir +"/configs/c.1/"+USB_FUNCTION_HID_KEYBOARD_name); !os.IsNotExist(err1) {
		result.Use_HID_KEYBOARD = true
	}

	//USB HID Mouse
	if _, err1 := os.Stat(gadgetDir +"/configs/c.1/"+USB_FUNCTION_HID_MOUSE_name); !os.IsNotExist(err1) {
		result.Use_HID_MOUSE = true
	}

	//USB HID Raw
	if _, err1 := os.Stat(gadgetDir +"/configs/c.1/"+USB_FUNCTION_HID_RAW_name); !os.IsNotExist(err1) {
		result.Use_HID_RAW = true
	}

	//USB Mass Storage
	if _, err1 := os.Stat(gadgetDir +"/configs/c.1/mass_storage.ms1"); !os.IsNotExist(err1) {
		result.Use_UMS = true
		result.UmsSettings = &pb.GadgetSettingsUMS{}

		//Check if running as CD-Rom
		if res, err := ioutil.ReadFile(gadgetDir + "/functions/mass_storage.ms1/lun.0/cdrom"); err != nil {
			err1 := errors.New(fmt.Sprintf("gadget %s error reading USB Mass Storage cdrom emulation state", gadgetName))
			return nil, err1
		} else {
			if strings.HasPrefix(string(res), "1") {
				result.UmsSettings.Cdrom = true
			} //else branche unneeded, as false is default
		}

		//Check name of backing file
		if res, err := ioutil.ReadFile(gadgetDir + "/functions/mass_storage.ms1/lun.0/file"); err != nil {
			err1 := errors.New(fmt.Sprintf("gadget %s error reading USB Mass Storage image file setting", gadgetName))
			return nil, err1
		} else {
			result.UmsSettings.File = strings.TrimSuffix(string(res), "\000\n")

			// remove path
			result.UmsSettings.File = filepath.Base(result.UmsSettings.File)
		}
	} else {
		result.UmsSettings = &pb.GadgetSettingsUMS{}
	}

	//check if UDC is set (Gadget enabled)
	udcName, _ := getUDCName()

	if res, err := ioutil.ReadFile(gadgetDir + "/UDC"); err != nil {
		err1 := errors.New(fmt.Sprintf("gadget %s error reading UDC", gadgetName))
		return nil, err1
	} else {
		udcNameSet := strings.TrimSuffix(string(res), "\n")
		//log.Printf("UDC test: udcNameSet %s, udcName %s", udcNameSet, udcName)
		if udcName == udcNameSet {
			result.Enabled = true
		}
	}

	return

}

// This command is working on the active gadget directly, so changes aren't reflected back
// to the GadgetSettingsState
func ensureUMSBackingFile(settings *pb.GadgetSettings) {
	if settings.UmsSettings == nil {
		settings.UmsSettings = &pb.GadgetSettingsUMS{}
	}
	if strings.TrimSpace(settings.UmsSettings.File) == "" {
		settings.UmsSettings.File = "default.bin"
	}
	if settings.UmsSettings.Cdrom {
		return
	}
	dir := common.PATH_IMAGE_FLASHDRIVE
	_ = os.MkdirAll(dir, 0755)
	path := dir + "/" + settings.UmsSettings.File
	if _, err := os.Stat(path); err == nil {
		return
	}
	log.Printf("USB UMS: creating %s (32MiB FAT)", path)
	f, err := os.Create(path)
	if err != nil {
		log.Printf("USB UMS: could not create %s: %v", path, err)
		return
	}
	_ = f.Truncate(32 << 20)
	_ = f.Close()
	if err := exec.Command("mkfs.vfat", "-n", "P4WNP1", path).Run(); err != nil {
		log.Printf("USB UMS: mkfs.vfat %s: %v (image still usable as raw)", path, err)
	}
}

func MountUMSFile(filename string) error {
	funcdir := USB_GADGET_DIR + "/functions/mass_storage.ms1"
	err := ioutil.WriteFile(funcdir+"/lun.0/file", []byte(filename), os.ModePerm)
	if err != nil {
		return errors.New(fmt.Sprintf("settings backing file for USB Mass Storage failed: %v", err))
	}
	return nil
}

func (gm *UsbGadgetManager) DeployGadgetSettings(settings *pb.GadgetSettings) (err error) {
	if !gm.Usable {
		return ErrUsbNotUsable
	}

	defer func() {
		if r := recover(); r != nil {
			log.Printf("USB gadget deploy panic (WiFi left running): %v", r)
			err = fmt.Errorf("USB gadget deploy panic: %v", r)
		}
	}()

	//Lock, only one change at a time
	gm.gadgetSettingsLock.Lock()
	defer gm.gadgetSettingsLock.Unlock()
	//fmt.Println("DeployGadgetSettings beyond lock ...")

	err = ValidateGadgetSetting(settings)
	if err != nil {
		return
	}

	if settings.Use_RNDIS && settings.Use_CDC_ECM {
		log.Printf("USB: RNDIS and CDC ECM/NCM both enabled; keeping RNDIS only so Windows does not drop the link")
		settings.Use_CDC_ECM = false
	}
	if settings.Use_UMS {
		ensureUMSBackingFile(settings)
	}

	//Crash fix: abort HID controller before destorying gadget, to avoid crashes by attempting to close filedescriptors of non existent files
	if gm.hidCtl != nil {
		gm.hidCtl.Abort()
	}

	fmt.Println("... deconstruct old gadget")
	//ToDo: Former gadgets are destroyed without testing if there're changes, this should be aborted if GadgetSettingsState == GetDeployedGadgetSettings()
	gm.DestroyGadget(USB_GADGET_NAME)

	var usesUSBEthernet bool

	gadgetRoot := USB_GADGET_DIR_BASE

	//check if root exists, return error otherwise
	if _, err := os.Stat(gadgetRoot); os.IsNotExist(err) {
		return errors.New("configfs path for gadget doesn't exist")
	}

	//ToDo: check if UDC is present and usable

	//create gadget folder
	os.Mkdir(USB_GADGET_DIR, os.ModePerm)
	log.Printf("Creating composite gadget '%s'\nSettings:\n%+v", USB_GADGET_NAME, settings)

	//set vendor ID, product ID
	ioutil.WriteFile(USB_GADGET_DIR+"/idVendor", []byte(settings.Vid), os.ModePerm)
	ioutil.WriteFile(USB_GADGET_DIR+"/idProduct", []byte(settings.Pid), os.ModePerm)

	//set USB mode to 2.0 and device version to 1.0
	ioutil.WriteFile(USB_GADGET_DIR+"/bcdUSB", []byte(USB_bcdUSB), os.ModePerm)
	ioutil.WriteFile(USB_GADGET_DIR+"/bcdDevice", []byte(USB_bcdDevice), os.ModePerm)

	//composite class / subclass / proto (needs single configuration)
	ioutil.WriteFile(USB_GADGET_DIR+"/bDeviceClass", []byte(USB_bDeviceClass), os.ModePerm)
	ioutil.WriteFile(USB_GADGET_DIR+"/bDeviceSubClass", []byte(USB_bDeviceSubClass), os.ModePerm)
	ioutil.WriteFile(USB_GADGET_DIR+"/bDeviceProtocol", []byte(USB_bDeviceProtocol), os.ModePerm)

	// set device descriptions
	os.Mkdir(USB_GADGET_DIR+"/strings/0x409", os.ModePerm) // English language strings
	ioutil.WriteFile(USB_GADGET_DIR+"/strings/0x409/serialnumber", []byte(settings.Serial), os.ModePerm)
	ioutil.WriteFile(USB_GADGET_DIR+"/strings/0x409/manufacturer", []byte(settings.Manufacturer), os.ModePerm)
	ioutil.WriteFile(USB_GADGET_DIR+"/strings/0x409/product", []byte(settings.Product), os.ModePerm)

	// create configuration instance (only one, as multiple configs aren't valid for Windows composite devices)
	os.MkdirAll(USB_GADGET_DIR+"/configs/c.1/strings/0x409", os.ModePerm) // English language strings
	ioutil.WriteFile(USB_GADGET_DIR+"/configs/c.1/strings/0x409/configuration", []byte("Config 1: Composite"), os.ModePerm)
	ioutil.WriteFile(USB_GADGET_DIR+"/configs/c.1/MaxPower", []byte(USB_CONFIGURATION_MaxPower), os.ModePerm)
	ioutil.WriteFile(USB_GADGET_DIR+"/configs/c.1/bmAttributes", []byte(USB_CONFIGURATION_bmAttributes), os.ModePerm)

	// RNDIS has to be the first interface on Composite device for Windows (first function initialized)
	if settings.Use_RNDIS {
		log.Printf("... creating USB RNDIS function")
		usesUSBEthernet = true
		os.Mkdir(USB_GADGET_DIR+"/functions/rndis.usb0", os.ModePerm) //create RNDIS function
		ioutil.WriteFile(USB_GADGET_DIR+"/functions/rndis.usb0/host_addr", []byte(settings.RndisSettings.HostAddr), os.ModePerm)
		ioutil.WriteFile(USB_GADGET_DIR+"/functions/rndis.usb0/dev_addr", []byte(settings.RndisSettings.DevAddr), os.ModePerm)

		/*
			add OS specific device descriptors to force Windows to load RNDIS drivers
			=============================================================================
			Witout this additional descriptors, most Windows system detect the RNDIS interface as "Serial COM port"
			To prevent this, the Microsoft specific OS descriptors are added in here
			!! Important:
			If the device already has been connected to the Windows System without providing the
			OS descriptor, Windows never asks again for them and thus never installs the RNDIS driver
			This behavior is driven by creation of an registry hive, the first time a device without
			OS descriptors is attached. The key is build like this:

			HKLM\SYSTEM\CurrentControlSet\Control\usbflags\[USB_VID+USB_PID+bcdRelease\osvc

			To allow Windows to read the OS descriptors again, the according registry hive has to be
			deleted manually or USB descriptor values have to be cahnged (f.e. USB_PID).
		*/

		//set OS descriptors for Windows
		ioutil.WriteFile(USB_GADGET_DIR+"/os_desc/use", []byte(USB_FUNCTION_RNDIS_os_desc_use), os.ModePerm)
		ioutil.WriteFile(USB_GADGET_DIR+"/os_desc/b_vendor_code", []byte(USB_FUNCTION_RNDIS_os_desc_b_vendor_code), os.ModePerm)
		ioutil.WriteFile(USB_GADGET_DIR+"/os_desc/qw_sign", []byte(USB_FUNCTION_RNDIS_os_desc_qw_sign), os.ModePerm)
		ioutil.WriteFile(USB_GADGET_DIR+"/functions/rndis.usb0/os_desc/interface.rndis/compatible_id", []byte(USB_FUNCTION_RNDIS_os_desc_interface_compatible_id), os.ModePerm)
		ioutil.WriteFile(USB_GADGET_DIR+"/functions/rndis.usb0/os_desc/interface.rndis/sub_compatible_id", []byte(USB_FUNCTION_RNDIS_os_desc_interface_sub_compatible_id), os.ModePerm)

		//activate function by symlinking to config 1
		err := os.Symlink(USB_GADGET_DIR+"/functions/rndis.usb0", USB_GADGET_DIR+"/configs/c.1/rndis.usb0")
		if err != nil {
			log.Println(err)
		}

		// add config 1 to OS descriptors
		err = os.Symlink(USB_GADGET_DIR+"/configs/c.1", USB_GADGET_DIR+"/os_desc/c.1")
		if err != nil {
			log.Println(err)
		}
	}

	if settings.Use_CDC_ECM {
		usesUSBEthernet = true
		ethFunc := "ncm.usb1"
		if err := os.Mkdir(USB_GADGET_DIR+"/functions/"+ethFunc, os.ModePerm); err != nil {
			log.Printf("NCM function not available (%v), falling back to CDC ECM", err)
			ethFunc = "ecm.usb1"
			_ = os.Mkdir(USB_GADGET_DIR+"/functions/"+ethFunc, os.ModePerm)
		}
		log.Printf("... creating USB %s function", ethFunc)
		ioutil.WriteFile(USB_GADGET_DIR+"/functions/"+ethFunc+"/host_addr", []byte(settings.CdcEcmSettings.HostAddr), os.ModePerm)
		ioutil.WriteFile(USB_GADGET_DIR+"/functions/"+ethFunc+"/dev_addr", []byte(settings.CdcEcmSettings.DevAddr), os.ModePerm)
		if err := os.Symlink(USB_GADGET_DIR+"/functions/"+ethFunc, USB_GADGET_DIR+"/configs/c.1/"+ethFunc); err != nil {
			log.Println(err)
		}
	}

	if settings.Use_SERIAL {
		log.Printf("... creating USB serial function")
		os.Mkdir(USB_GADGET_DIR+"/functions/acm.GS0", os.ModePerm) //create ACM function

		//activate function by symlinking to config 1
		err := os.Symlink(USB_GADGET_DIR+"/functions/acm.GS0", USB_GADGET_DIR+"/configs/c.1/acm.GS0")
		if err != nil {
			log.Println(err)
		}

	}

	if settings.Use_HID_KEYBOARD {
		log.Printf("... creating USB HID Keyboard function")
		funcdir := USB_GADGET_DIR + "/functions/" + USB_FUNCTION_HID_KEYBOARD_name
		os.Mkdir(funcdir, os.ModePerm) //create HID function for keyboard

		ioutil.WriteFile(funcdir+"/protocol", []byte(USB_FUNCTION_HID_KEYBOARD_protocol), os.ModePerm)
		ioutil.WriteFile(funcdir+"/subclass", []byte(USB_FUNCTION_HID_KEYBOARD_subclass), os.ModePerm)
		ioutil.WriteFile(funcdir+"/report_length", []byte(USB_FUNCTION_HID_KEYBOARD_report_length), os.ModePerm)
		ioutil.WriteFile(funcdir+"/report_desc", []byte(USB_FUNCTION_HID_KEYBOARD_report_desc), os.ModePerm)

		err := os.Symlink(funcdir, USB_GADGET_DIR+"/configs/c.1/"+USB_FUNCTION_HID_KEYBOARD_name)
		if err != nil {
			log.Println(err)
		}
	}

	if settings.Use_HID_MOUSE {
		log.Printf("... creating USB HID Mouse function")
		funcdir := USB_GADGET_DIR + "/functions/" + USB_FUNCTION_HID_MOUSE_name
		os.Mkdir(funcdir, os.ModePerm) //create HID function for mouse

		ioutil.WriteFile(funcdir+"/protocol", []byte(USB_FUNCTION_HID_MOUSE_protocol), os.ModePerm)
		ioutil.WriteFile(funcdir+"/subclass", []byte(USB_FUNCTION_HID_MOUSE_subclass), os.ModePerm)
		ioutil.WriteFile(funcdir+"/report_length", []byte(USB_FUNCTION_HID_MOUSE_report_length), os.ModePerm)
		ioutil.WriteFile(funcdir+"/report_desc", []byte(USB_FUNCTION_HID_MOUSE_report_desc), os.ModePerm)

		err := os.Symlink(funcdir, USB_GADGET_DIR+"/configs/c.1/"+USB_FUNCTION_HID_MOUSE_name)
		if err != nil {
			log.Println(err)
		}
	}

	if settings.Use_HID_RAW {
		log.Printf("... creating USB HID Generic device function")
		funcdir := USB_GADGET_DIR + "/functions/" + USB_FUNCTION_HID_RAW_name
		os.Mkdir(funcdir, os.ModePerm) //create HID function for mouse

		ioutil.WriteFile(funcdir+"/protocol", []byte(USB_FUNCTION_HID_RAW_protocol), os.ModePerm)
		ioutil.WriteFile(funcdir+"/subclass", []byte(USB_FUNCTION_HID_RAW_subclass), os.ModePerm)
		ioutil.WriteFile(funcdir+"/report_length", []byte(USB_FUNCTION_HID_RAW_report_length), os.ModePerm)
		ioutil.WriteFile(funcdir+"/report_desc", []byte(USB_FUNCTION_HID_RAW_report_desc), os.ModePerm)

		err := os.Symlink(funcdir, USB_GADGET_DIR+"/configs/c.1/"+USB_FUNCTION_HID_RAW_name)
		if err != nil {
			log.Println(err)
		}
	}

	if settings.Use_UMS {
		log.Printf("... creating USB Mass Storage device function")
		funcdir := USB_GADGET_DIR + "/functions/mass_storage.ms1"
		os.Mkdir(funcdir, os.ModePerm) //create HID function for mouse

		ioutil.WriteFile(funcdir+"/stall", []byte("1"), os.ModePerm) // Allow bulk Endpoints
		if settings.UmsSettings.Cdrom {
			ioutil.WriteFile(funcdir+"/lun.0/cdrom", []byte("1"), os.ModePerm) // CD-Rom
		} else {
			ioutil.WriteFile(funcdir+"/lun.0/cdrom", []byte("0"), os.ModePerm) // Writable flashdrive
		}

		ioutil.WriteFile(funcdir+"/lun.0/ro", []byte("0"), os.ModePerm) // Don't restrict to read-only (is implied by cdrom=1 if needed, but causes issues on backend FS if enabled)

		// enable Force Unit Access (FUA) to make Windows write synchronously
		// this is slow, but unplugging the stick without unmounting works
		ioutil.WriteFile(funcdir+"/lun.0/nofua", []byte("0"), os.ModePerm) // Don't restrict to read-only (is implied by cdrom=1 if needed, but causes issues on backend FS if enabled)

		//Provide the backing image
		file := settings.UmsSettings.File
		if settings.UmsSettings.Cdrom {
			file = common.PATH_IMAGE_CDROM + "/" + file
		} else {
			file = common.PATH_IMAGE_FLASHDRIVE + "/" + file
		}
		ioutil.WriteFile(funcdir+"/lun.0/file", []byte(file), os.ModePerm) // Set backing file (or block device) for USB Mass Storage

		err := os.Symlink(funcdir, USB_GADGET_DIR+"/configs/c.1/"+"mass_storage.ms1")
		if err != nil {
			log.Println(err)
		}
	}

	//clear device path for HID devices
	gm.State.DevicePath[USB_FUNCTION_HID_KEYBOARD_name] = ""
	gm.State.DevicePath[USB_FUNCTION_HID_MOUSE_name] = ""
	gm.State.DevicePath[USB_FUNCTION_HID_RAW_name] = ""

	//get UDC driver name and bind to gadget
	if settings.Enabled {
		udcName, err := getUDCName()
		if err != nil {
			return err
		}
		logSelectedUDC()
		_ = ioutil.WriteFile(USB_GADGET_DIR+"/UDC", []byte("\x00"), os.ModePerm)
		time.Sleep(300 * time.Millisecond)
		setUSBRoleDevice()
		log.Printf("Enabeling gadget for UDC: %s\n", udcName)
		if err = ioutil.WriteFile(USB_GADGET_DIR+"/UDC", []byte(udcName), os.ModePerm); err != nil {
			fmt.Println("... error enabling gadget:", err)
			return err
		}
		if bound, err := ioutil.ReadFile(USB_GADGET_DIR + "/UDC"); err == nil {
			log.Printf("Gadget bound to UDC %q", strings.TrimSpace(string(bound)))
		}
		time.Sleep(400 * time.Millisecond)
		logUDCState(udcName)
		st := ""
		if b, err := ioutil.ReadFile("/sys/class/udc/" + udcName + "/state"); err == nil {
			st = strings.TrimSpace(string(b))
		}
		if st == "not attached" || st == "" {
			log.Printf("USB UDC still %q after bind; forcing device mode again", st)
			setUSBRoleDevice()
			time.Sleep(300 * time.Millisecond)
			logUDCState(udcName)
		}
		logGadgetLinkState()
		gm.startUDCWatch(udcName)

		//update device path
		_ = exec.Command("udevadm", "settle", "--timeout=2").Run()
		log.Println("Retrieving path to HID devices")
		gm.State.DevicePath[USB_FUNCTION_HID_KEYBOARD_name] = waitHIDDevicePath(USB_FUNCTION_HID_KEYBOARD_name, "/dev/hidg0")
		gm.State.DevicePath[USB_FUNCTION_HID_MOUSE_name] = waitHIDDevicePath(USB_FUNCTION_HID_MOUSE_name, "/dev/hidg1")
		gm.State.DevicePath[USB_FUNCTION_HID_RAW_name] = waitHIDDevicePath(USB_FUNCTION_HID_RAW_name, "")

		if settings.Use_HID_KEYBOARD || settings.Use_HID_MOUSE {
			if errH := gm.initHIDControllerLocked(); errH != nil {
				log.Printf("ERROR: HID controller: %v", errH)
			}
		} else {
			if gm.hidCtl != nil { gm.hidCtl.Abort() }
			gm.hidCtl = nil
			log.Printf("HIDController for keyboard / mouse disabled\n")
		}
	}




	deleteUSBEthernetBridge() //delete former used bridge, if there's any
	//In case USB ethernet is uesd (RNDIS or CDC ECM), we add a bridge interface
	if usesUSBEthernet && settings.Enabled {
		//wait till "usb0" or "usb1" comes up
		err := pollForUSBEthernet(10*time.Second)
		if err == nil {
			//add USBEthernet bridge including the usb interfaces
			log.Printf("... creating network bridge for USB ethernet devices")
			addUSBEthernetBridge()
			log.Printf("... checking for stored network interface settings for USB ethernet")
			//ReInitNetworkInterface(USB_ETHERNET_BRIDGE_NAME)
			if nim,err := gm.RootSvc.SubSysNetwork.GetManagedInterface(USB_ETHERNET_BRIDGE_NAME); err == nil {
				nim.ReDeploy()
			}

		} else {
			return err
		}

	}

	log.Printf("... done")
	return nil
}

func (gm *UsbGadgetManager) stopUDCWatch() {
	if gm.udcWatchStop != nil {
		close(gm.udcWatchStop)
		gm.udcWatchStop = nil
	}
}

func (gm *UsbGadgetManager) startUDCWatch(udcName string) {
	gm.stopUDCWatch()
	stop := make(chan struct{})
	gm.udcWatchStop = stop
	go gm.watchUDCReconnect(udcName, stop)
}

func rebindGadgetUDC(udcName string) {
	log.Printf("USB: rebinding UDC %s for a clean host enumeration", udcName)
	_ = ioutil.WriteFile(USB_GADGET_DIR+"/UDC", []byte("\x00"), os.ModePerm)
	time.Sleep(400 * time.Millisecond)
	if err := ioutil.WriteFile(USB_GADGET_DIR+"/UDC", []byte(udcName), os.ModePerm); err != nil {
		log.Printf("USB: UDC rebind failed: %v", err)
		return
	}
	time.Sleep(300 * time.Millisecond)
	logGadgetLinkState()
}

func (gm *UsbGadgetManager) watchUDCReconnect(udcName string, stop <-chan struct{}) {
	statePath := "/sys/class/udc/" + udcName + "/state"
	prev := ""
	sawDetach := false
	ignoreUntil := time.Time{}
	for {
		select {
		case <-stop:
			return
		case <-time.After(400 * time.Millisecond):
		}
		b, err := ioutil.ReadFile(statePath)
		if err != nil {
			continue
		}
		state := strings.TrimSpace(string(b))
		if state != prev && prev != "" {
			log.Printf("USB UDC %s state %s -> %s", udcName, prev, state)
		}
		if time.Now().Before(ignoreUntil) {
			prev = state
			continue
		}
		if state == "not attached" {
			sawDetach = true
		} else if sawDetach && state != "not attached" {
			sawDetach = false
			ignoreUntil = time.Now().Add(4 * time.Second)
			log.Printf("USB: host attached after disconnect; rebinding UDC so Windows gets a fresh RNDIS enum")
			rebindGadgetUDC(udcName)
		}
		prev = state
	}
}

func waitHIDDevicePath(funcName, fallback string) string {
	deadline := time.Now().Add(3 * time.Second)
	for {
		if p, err := enumDevicePath(funcName); err == nil && p != "" {
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
		if fallback != "" {
			if _, err := os.Stat(fallback); err == nil {
				return fallback
			}
		}
		if time.Now().After(deadline) {
			if fallback != "" {
				if _, err := os.Stat(fallback); err == nil {
					return fallback
				}
			}
			return ""
		}
		time.Sleep(150 * time.Millisecond)
	}
}

func (gm *UsbGadgetManager) initHIDControllerLocked() error {
	kbd := gm.State.DevicePath[USB_FUNCTION_HID_KEYBOARD_name]
	mouse := gm.State.DevicePath[USB_FUNCTION_HID_MOUSE_name]
	if kbd == "" {
		kbd = waitHIDDevicePath(USB_FUNCTION_HID_KEYBOARD_name, "/dev/hidg0")
		gm.State.DevicePath[USB_FUNCTION_HID_KEYBOARD_name] = kbd
	}
	if mouse == "" {
		mouse = waitHIDDevicePath(USB_FUNCTION_HID_MOUSE_name, "/dev/hidg1")
		gm.State.DevicePath[USB_FUNCTION_HID_MOUSE_name] = mouse
	}
	if kbd == "" && mouse == "" {
		return fmt.Errorf("no HID gadget device nodes (expected /dev/hidg0)")
	}
	mapPath := common.PATH_KEYBOARD_LANGUAGE_MAPS
	if _, err := os.Stat(mapPath); err != nil {
		return fmt.Errorf("keyboard maps missing at %s (copy dist/keymaps there): %v", mapPath, err)
	}
	ctl, errH := hid.NewHIDController(context.Background(), kbd, mapPath, mouse)
	if errH != nil || ctl == nil {
		gm.hidCtl = nil
		return fmt.Errorf("keyboard %q mouse %q maps %q: %v", kbd, mouse, mapPath, errH)
	}
	ctl.SetEventHandler(gm)
	gm.hidCtl = ctl
	log.Printf("HIDController for keyboard: '%s', mouse: '%s' initialized", kbd, mouse)
	return nil
}

func enumDevicePath(funcName string) (devPath string, err error){
	//cat /sys/dev/char/$(cat /sys/kernel/config/usb_gadget/mame82_gadget/functions/hid.mouse/dev)/uevent | grep DEVNAME
	devfile := USB_GADGET_DIR + "/functions/" + funcName + "/dev"

	var udevNode string
	if res, err := ioutil.ReadFile(devfile); err != nil {
		err1 := errors.New(fmt.Sprintf("Gadget error reading udevname for %s\n", funcName))
		return "", err1
	} else {
		udevNode = strings.TrimSuffix(string(res), "\n")
	}


	ueventPath := fmt.Sprintf("/sys/dev/char/%s/uevent", udevNode)
	if ueventContent, err := ioutil.ReadFile(ueventPath); err != nil {
		err1 := errors.New(fmt.Sprintf("Gadget error reading uevent file '%s' for %s\n", ueventPath, funcName))
		return "", err1
	} else {

		strDevNameSub := rp_usbHidDevName.FindStringSubmatch(string(ueventContent))
		if len(strDevNameSub) > 1 { devPath = "/dev/" + strDevNameSub[1]}
	}

	return
}

func (gm *UsbGadgetManager) DestroyAllGadgets() error {

	//gadgetRoot := "./test"
	gadgetRoot := USB_GADGET_DIR_BASE

	//check if root exists, return error otherwise
	if _, err := os.Stat(gadgetRoot); os.IsNotExist(err) {
		return errors.New("configfs path for gadget doesn't exist")
	}

	gadgetDirs, err := ioutil.ReadDir(gadgetRoot)
	if err != nil {
		return errors.New("no gadgets")
	}

	for _, gadgetDirObj := range gadgetDirs {
		gadgetName := gadgetDirObj.Name()
		log.Println("Found gadget: " + gadgetName)

		err = gm.DestroyGadget(gadgetName)
		if err != nil {
			log.Println(err) //don't return, continue with next
		}
	}

	if gm.hidCtl != nil { gm.hidCtl.Abort() }
	gm.hidCtl = nil
	log.Printf("HIDController for keyboard / mouse disabled\n")

	return nil
}

func (gm *UsbGadgetManager) DestroyGadget(gadgetName string) error {
	gm.stopUDCWatch()




	gadgetDir := USB_GADGET_DIR_BASE + "/" + gadgetName

	//check if root exists, return error otherwise
	if _, err := os.Stat(USB_GADGET_DIR_BASE); os.IsNotExist(err) {
		return errors.New("Gadget " + gadgetName + " doesn't exist")
	}
	log.Println("Deconstructing gadget " + gadgetName + "...")

	//Assure gadget gets unbound from UDC
	ioutil.WriteFile(gadgetDir+"/UDC", []byte("\x00"), os.ModePerm)

	//Iterate over configurations
	configDirs, _ := ioutil.ReadDir(gadgetDir + "/configs")
	for _, confDirObj := range configDirs {
		confName := confDirObj.Name()
		confDir := gadgetDir + "/configs/" + confName
		log.Println("Found config: " + confName)

		//find linked functions
		confContents, _ := ioutil.ReadDir(confDir)
		for _, function := range confContents {
			//Remove link from function to config
			if function.Mode()&os.ModeSymlink > 0 {
				log.Println("\tRemoving function " + function.Name() + " from config " + confName)
				os.Remove(confDir + "/" + function.Name())
			}
		}

		//find string directories in config
		stringsContents, _ := ioutil.ReadDir(confDir + "/strings")
		for _, str := range stringsContents {
			stringDir := str.Name()
			//Remove string from config
			log.Println("\tRemoving string dir '" + stringDir + "' from configuration")
			os.Remove(confDir + "/strings/" + stringDir)
		}

		//Check if there's an OS descriptor refering this config
		if _, err := os.Stat(gadgetDir + "/os_desc/" + confName); !os.IsNotExist(err) {
			log.Println("\tDeleting link to '" + confName + "' from gadgets OS descriptor")
			os.Remove(gadgetDir + "/os_desc/" + confName)
		}

		// remove config folder, finally
		log.Println("\tDeleting configuration '" + confName + "'")
		os.Remove(confDir)
	}

	// remove functions
	log.Println("Removing functions from '" + gadgetName + "'")
	os.RemoveAll(gadgetDir + "/functions/")

	//find string directories in gadget
	stringsContents, _ := ioutil.ReadDir(gadgetDir + "/strings")
	for _, str := range stringsContents {
		stringDir := str.Name()
		//Remove string from config
		log.Println("Removing string dir '" + stringDir + "' from " + gadgetName)
		os.Remove(gadgetDir + "/strings/" + stringDir)
	}

	//And now remove the gadget itself
	log.Println("Removing gadget " + gadgetName)
	os.Remove(gadgetDir)

	return nil
}
