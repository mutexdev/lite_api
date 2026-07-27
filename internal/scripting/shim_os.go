package scripting

// The sandbox node:os and process shims.
//
// Split out of scripting.go by AST: declarations are identified by the parser
// and copied verbatim from their source offsets.

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"time"

	"github.com/mutexdev/lite_api/internal/interp"
	"github.com/mutexdev/lite_api/internal/types"

	"github.com/dop251/goja"
)

func newScriptOSObject(runtime *goja.Runtime) *goja.Object {
	osObject := runtime.NewObject()
	_ = osObject.Set("EOL", scriptOSEOL())
	_ = osObject.Set("devNull", scriptOSDevNull())
	_ = osObject.Set("constants", map[string]interface{}{
		"signals": map[string]int{
			"SIGHUP": 1, "SIGINT": 2, "SIGQUIT": 3, "SIGILL": 4, "SIGTRAP": 5,
			"SIGABRT": 6, "SIGBUS": 7, "SIGFPE": 8, "SIGKILL": 9, "SIGUSR1": 10,
			"SIGSEGV": 11, "SIGUSR2": 12, "SIGPIPE": 13, "SIGALRM": 14, "SIGTERM": 15,
		},
		"errno": map[string]int{
			"EACCES": 13, "EADDRINUSE": 48, "ECONNREFUSED": 61, "ECONNRESET": 54,
			"EEXIST": 17, "EINVAL": 22, "ENOENT": 2, "ENOTDIR": 20, "EPERM": 1,
			"ETIMEDOUT": 60,
		},
		"priority": map[string]int{
			"PRIORITY_LOW": 19, "PRIORITY_BELOW_NORMAL": 10, "PRIORITY_NORMAL": 0,
			"PRIORITY_ABOVE_NORMAL": -7, "PRIORITY_HIGH": -14, "PRIORITY_HIGHEST": -20,
		},
	})
	_ = osObject.Set("arch", func() string { return scriptNodeArch() })
	_ = osObject.Set("availableParallelism", func() int {
		if count := goruntime.NumCPU(); count > 0 {
			return count
		}
		return 1
	})
	_ = osObject.Set("cpus", func() []map[string]interface{} {
		count := goruntime.NumCPU()
		if count < 1 {
			count = 1
		}
		cpus := make([]map[string]interface{}, count)
		for index := range cpus {
			cpus[index] = map[string]interface{}{
				"model": scriptOSCPUModel(),
				"speed": 0,
				"times": map[string]int64{"user": 0, "nice": 0, "sys": 0, "idle": 0, "irq": 0},
			}
		}
		return cpus
	})
	_ = osObject.Set("endianness", func() string { return "LE" })
	_ = osObject.Set("freemem", func() float64 {
		var stats goruntime.MemStats
		goruntime.ReadMemStats(&stats)
		free := scriptOSTotalMem() - float64(stats.Alloc)
		if free < 1 {
			return 1
		}
		return free
	})
	_ = osObject.Set("getPriority", func(...int) int { return 0 })
	_ = osObject.Set("homedir", scriptOSHomeDir)
	_ = osObject.Set("hostname", scriptOSHostname)
	_ = osObject.Set("loadavg", func() []float64 { return []float64{0, 0, 0} })
	_ = osObject.Set("machine", func() string { return goruntime.GOARCH })
	_ = osObject.Set("networkInterfaces", scriptOSNetworkInterfaces)
	_ = osObject.Set("platform", func() string { return scriptNodePlatform() })
	_ = osObject.Set("release", scriptOSRelease)
	_ = osObject.Set("setPriority", func(...int) goja.Value { return goja.Undefined() })
	_ = osObject.Set("tmpdir", func() string { return os.TempDir() })
	_ = osObject.Set("totalmem", scriptOSTotalMem)
	_ = osObject.Set("type", scriptOSType)
	_ = osObject.Set("uptime", func() float64 {
		elapsed := time.Since(scriptOSStartTime).Seconds()
		if elapsed < 1 {
			return 1
		}
		return elapsed
	})
	_ = osObject.Set("userInfo", scriptOSUserInfo)
	_ = osObject.Set("version", scriptOSVersion)
	return osObject
}

func scriptOSEOL() string {
	if goruntime.GOOS == "windows" {
		return "\r\n"
	}
	return "\n"
}

func scriptOSDevNull() string {
	if goruntime.GOOS == "windows" {
		return "\\\\.\\nul"
	}
	return "/dev/null"
}

func scriptOSCPUModel() string {
	switch goruntime.GOARCH {
	case "arm64":
		return "arm64 CPU"
	case "amd64":
		return "x64 CPU"
	default:
		return goruntime.GOARCH + " CPU"
	}
}

func scriptOSType() string {
	switch goruntime.GOOS {
	case "darwin":
		return "Darwin"
	case "linux":
		return "Linux"
	case "windows":
		return "Windows_NT"
	default:
		return goruntime.GOOS
	}
}

func scriptOSRelease() string {
	if output, err := exec.Command("uname", "-r").Output(); err == nil {
		if release := strings.TrimSpace(string(output)); release != "" {
			return release
		}
	}
	return goruntime.GOOS
}

func scriptOSVersion() string {
	if output, err := exec.Command("uname", "-v").Output(); err == nil {
		if version := strings.TrimSpace(string(output)); version != "" {
			return version
		}
	}
	return scriptOSType() + " " + scriptOSRelease()
}

func scriptOSHomeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

func scriptOSHostname() string {
	hostname, err := os.Hostname()
	if err != nil {
		return ""
	}
	return hostname
}

func scriptOSTotalMem() float64 {
	var stats goruntime.MemStats
	goruntime.ReadMemStats(&stats)
	total := float64(goruntime.NumCPU()) * 1024 * 1024 * 1024
	if total < float64(stats.Sys) {
		total = float64(stats.Sys)
	}
	if total < 1 {
		return 1
	}
	return total
}

func scriptOSNetworkInterfaces() map[string][]map[string]interface{} {
	result := map[string][]map[string]interface{}{}
	interfaces, err := net.Interfaces()
	if err != nil {
		return result
	}
	for _, iface := range interfaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			value, ok := scriptOSNetworkAddress(iface, addr)
			if ok {
				result[iface.Name] = append(result[iface.Name], value)
			}
		}
	}
	return result
}

func scriptOSNetworkAddress(iface net.Interface, addr net.Addr) (map[string]interface{}, bool) {
	var ip net.IP
	netmask := ""
	cidr := addr.String()
	switch value := addr.(type) {
	case *net.IPNet:
		ip = value.IP
		netmask = net.IP(value.Mask).String()
	case *net.IPAddr:
		ip = value.IP
	default:
		return nil, false
	}
	family := "IPv6"
	if ipv4 := ip.To4(); ipv4 != nil {
		ip = ipv4
		family = "IPv4"
	}
	if ip == nil {
		return nil, false
	}
	return map[string]interface{}{
		"address":  ip.String(),
		"netmask":  netmask,
		"family":   family,
		"mac":      iface.HardwareAddr.String(),
		"internal": iface.Flags&net.FlagLoopback != 0,
		"cidr":     cidr,
	}, true
}

func scriptOSUserInfo() map[string]interface{} {
	username := firstNonEmptyEnv("USER", "USERNAME", "LOGNAME")
	home := scriptOSHomeDir()
	if username == "" && home != "" {
		username = filepath.Base(home)
	}
	return map[string]interface{}{
		"username": username,
		"homedir":  home,
		"shell":    os.Getenv("SHELL"),
	}
}

func installScriptProcess(runtime *goja.Runtime, loop *scriptEventLoop, collectionPath string, processEnv map[string]string, sandboxMode string) {
	_ = runtime.Set("global", runtime.GlobalObject())
	if NormalizeJSSandboxMode(sandboxMode) != "developer" {
		return
	}
	processObject := runtime.NewObject()
	_ = processObject.Set("version", "v20.0.0")
	_ = processObject.Set("versions", map[string]string{"node": "20.0.0"})
	_ = processObject.Set("platform", scriptNodePlatform())
	_ = processObject.Set("arch", scriptNodeArch())
	_ = processObject.Set("env", scriptProcessEnv(processEnv))
	_ = processObject.Set("cwd", func() string { return collectionPath })
	_ = processObject.Set("nextTick", func(call goja.FunctionCall) goja.Value {
		if loop != nil {
			loop.queueNextTick(call.Argument(0), call.Arguments[1:]...)
		}
		return goja.Undefined()
	})
	_ = runtime.Set("process", processObject)
}

func ProcessEnvForCollection(collection *types.Collection, workspacePath string) map[string]string {
	env := scriptProcessEnv(nil)
	if collection == nil {
		return env
	}
	if strings.TrimSpace(workspacePath) == "" && strings.TrimSpace(collection.Path) != "" {
		workspacePath = filepath.Dir(filepath.Clean(collection.Path))
	}
	mergeStringMap(env, readDotEnvFile(filepath.Join(workspacePath, ".env")))
	mergeStringMap(env, readDotEnvFile(filepath.Join(collection.Path, ".env")))
	return env
}

func addProcessEnvVars(vars map[string]string, processEnv map[string]string) {
	for name, value := range processEnv {
		vars[interp.ProcessEnvPrefix+name] = value
	}
}

func scriptProcessEnv(overrides map[string]string) map[string]string {
	return interp.ScriptProcessEnv(overrides)
}

var scriptOSStartTime = time.Now()
