// Package agent provides system information collection for the provider agent.
package agent

import (
	"bufio"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/nubro999/worldland-gpu/internal/provider"
)

// SystemScanner collects system hardware information.
//빈구조체로 정의한 이유: 구조체를 정의하지 않으면 nil 포인터를 사용해야 함
type SystemScanner struct{}

// NewSystemScanner creates a new SystemScanner.
func NewSystemScanner() *SystemScanner {
	return &SystemScanner{}
} 

// Scan collects all system information. 
func (s *SystemScanner) Scan() (*provider.SystemSpec, error) { //heap에 할당된 메모리에 저장
	spec := &provider.SystemSpec{
		Architecture: runtime.GOARCH, //현재 바이너리가 컴파일된 CPU아키텍처
	}

	// Hostname
	hostname, _ := os.Hostname() //에러처리 무시
	spec.Hostname = hostname

	// OS info
	s.scanOSInfo(spec)

	// CPU info
	s.scanCPUInfo(spec)

	// Memory info
	s.scanMemoryInfo(spec)

	// GPU info
	s.scanGPUInfo(spec) //nvidia-smi 명령어 실행

	// Disk info
	s.scanDiskInfo(spec)

	// Network info
	s.scanNetworkInfo(spec) //ip addr 명령어 실행 - 외부 네트워크 정보

	return spec, nil //단일 책임 원칙 설계
}

func (s *SystemScanner) scanOSInfo(spec *provider.SystemSpec) {
	// Try to read /etc/os-release
	if data, err := os.ReadFile("/etc/os-release"); err == nil {
		lines := strings.Split(string(data), "\n")
		for _, line := range lines { //_ 인덱스 무시
			if strings.HasPrefix(line, "PRETTY_NAME=") {
				spec.OS = strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), "\"")
				break
			}
		}
	}

	// Kernel version
	if output, err := exec.Command("uname", "-r").Output(); err == nil {
		spec.KernelVer = strings.TrimSpace(string(output))
	}
}

func (s *SystemScanner) scanCPUInfo(spec *provider.SystemSpec) {
	file, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	coreCount := 0

	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "model name") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				spec.CPUModel = strings.TrimSpace(parts[1])
			}
		}
		if strings.HasPrefix(line, "processor") {
			coreCount++
		}
	}

	spec.CPUThreads = coreCount

	// Get physical cores
	if output, err := exec.Command("nproc").Output(); err == nil {
		if cores, err := strconv.Atoi(strings.TrimSpace(string(output))); err == nil {
			spec.CPUCores = cores
		}
	}
}

func (s *SystemScanner) scanMemoryInfo(spec *provider.SystemSpec) {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "MemTotal:") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				if kb, err := strconv.ParseInt(parts[1], 10, 64); err == nil {
					spec.TotalMemoryMB = kb / 1024
				}
			}
			break
		}
	}
}

func (s *SystemScanner) scanGPUInfo(spec *provider.SystemSpec) {
	// Try nvidia-smi
	cmd := exec.Command("nvidia-smi", "--query-gpu=index,name,memory.total,uuid,driver_version,pci.bus_id",
		"--format=csv,noheader,nounits")
	output, err := cmd.Output()
	if err != nil {
		return
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	for _, line := range lines {
		parts := strings.Split(line, ", ")
		if len(parts) < 6 {
			continue
		}

		index, _ := strconv.Atoi(strings.TrimSpace(parts[0]))
		memMB, _ := strconv.ParseInt(strings.TrimSpace(parts[2]), 10, 64)

		gpu := provider.GPUInfo{
			Index:     index,
			Name:      strings.TrimSpace(parts[1]),
			MemoryMB:  memMB,
			UUID:      strings.TrimSpace(parts[3]),
			DriverVer: strings.TrimSpace(parts[4]),
			PCIBusID:  strings.TrimSpace(parts[5]),
		}

		// Get CUDA version
		if cudaOutput, err := exec.Command("nvcc", "--version").Output(); err == nil {
			re := regexp.MustCompile(`release (\d+\.\d+)`)
			if matches := re.FindStringSubmatch(string(cudaOutput)); len(matches) > 1 {
				gpu.CUDAVersion = matches[1]
			}
		}

		spec.GPUs = append(spec.GPUs, gpu)
	}

	spec.TotalGPUs = len(spec.GPUs)
}

func (s *SystemScanner) scanDiskInfo(spec *provider.SystemSpec) {
	output, err := exec.Command("df", "-BG", "/").Output()
	if err != nil {
		return
	}

	lines := strings.Split(string(output), "\n")
	if len(lines) >= 2 {
		fields := strings.Fields(lines[1])
		if len(fields) >= 4 {
			// Total disk (remove 'G' suffix)
			if total, err := strconv.ParseInt(strings.TrimSuffix(fields[1], "G"), 10, 64); err == nil {
				spec.TotalDiskGB = total
			}
			// Available disk
			if avail, err := strconv.ParseInt(strings.TrimSuffix(fields[3], "G"), 10, 64); err == nil {
				spec.AvailableDiskGB = avail
			}
		}
	}
}

func (s *SystemScanner) scanNetworkInfo(spec *provider.SystemSpec) {
	// Get public IP
	client := &http.Client{Timeout: 5 * time.Second}
	if resp, err := client.Get("https://api.ipify.org"); err == nil {
		defer resp.Body.Close()
		if buf := make([]byte, 64); err == nil {
			n, _ := resp.Body.Read(buf)
			spec.PublicIP = strings.TrimSpace(string(buf[:n]))
		}
	}

	// Get private IP
	if output, err := exec.Command("hostname", "-I").Output(); err == nil {
		ips := strings.Fields(string(output))
		if len(ips) > 0 {
			spec.PrivateIP = ips[0]
		}
	}
}

// FormatSpec formats the spec for display.
func FormatSpec(spec *provider.SystemSpec) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("=== System Specification ===\n"))
	sb.WriteString(fmt.Sprintf("Hostname:     %s\n", spec.Hostname))
	sb.WriteString(fmt.Sprintf("OS:           %s\n", spec.OS))
	sb.WriteString(fmt.Sprintf("Kernel:       %s\n", spec.KernelVer))
	sb.WriteString(fmt.Sprintf("Architecture: %s\n\n", spec.Architecture))

	sb.WriteString(fmt.Sprintf("=== CPU ===\n"))
	sb.WriteString(fmt.Sprintf("Model:   %s\n", spec.CPUModel))
	sb.WriteString(fmt.Sprintf("Cores:   %d\n", spec.CPUCores))
	sb.WriteString(fmt.Sprintf("Threads: %d\n\n", spec.CPUThreads))

	sb.WriteString(fmt.Sprintf("=== Memory ===\n"))
	sb.WriteString(fmt.Sprintf("Total: %d MB (%.1f GB)\n\n", spec.TotalMemoryMB, float64(spec.TotalMemoryMB)/1024))

	sb.WriteString(fmt.Sprintf("=== GPU (%d total) ===\n", spec.TotalGPUs))
	for _, gpu := range spec.GPUs {
		sb.WriteString(fmt.Sprintf("  [%d] %s - %d MB - Driver: %s\n",
			gpu.Index, gpu.Name, gpu.MemoryMB, gpu.DriverVer))
	}
	sb.WriteString("\n")

	sb.WriteString(fmt.Sprintf("=== Storage ===\n"))
	sb.WriteString(fmt.Sprintf("Total:     %d GB\n", spec.TotalDiskGB))
	sb.WriteString(fmt.Sprintf("Available: %d GB\n\n", spec.AvailableDiskGB))

	sb.WriteString(fmt.Sprintf("=== Network ===\n"))
	sb.WriteString(fmt.Sprintf("Public IP:  %s\n", spec.PublicIP))
	sb.WriteString(fmt.Sprintf("Private IP: %s\n", spec.PrivateIP))

	return sb.String()
}
