package sdk

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Validator checks system requirements before installation.
type Validator struct {
	config *Config
}

// NewValidator creates a new Validator.
func NewValidator(config *Config) *Validator {
	return &Validator{config: config}
}

// ValidateAll runs all validation checks.
func (v *Validator) ValidateAll(ctx context.Context) (*ValidationResult, error) {
	result := &ValidationResult{
		Valid:  true,
		Checks: make([]ValidationCheck, 0),
	}

	// Check OS
	v.checkOS(result)

	// Check root/sudo
	v.checkPrivileges(result)

	// Check NVIDIA driver
	v.checkNVIDIADriver(result)

	// Check network connectivity
	v.checkNetwork(ctx, result)

	// Check required ports
	v.checkPorts(result)

	// Check disk space
	v.checkDiskSpace(result)

	// Check memory
	v.checkMemory(result)

	// Determine overall validity
	for _, check := range result.Checks {
		if !check.Passed {
			// Some checks are warnings, not errors
			if v.isRequiredCheck(check.Name) {
				result.Valid = false
				result.Errors = append(result.Errors, check.Message)
			} else {
				result.Warnings = append(result.Warnings, check.Message)
			}
		}
	}

	return result, nil
}

func (v *Validator) isRequiredCheck(name string) bool {
	required := map[string]bool{
		"os":             true,
		"privileges":     true,
		"nvidia_driver":  true,
		"network_master": true,
	}
	return required[name]
}

func (v *Validator) checkOS(result *ValidationResult) {
	check := ValidationCheck{Name: "os"}

	if runtime.GOOS != "linux" {
		check.Passed = false
		check.Message = fmt.Sprintf("Unsupported OS: %s. Linux required.", runtime.GOOS)
	} else {
		check.Passed = true
		check.Message = fmt.Sprintf("OS: %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	result.Checks = append(result.Checks, check)
}

func (v *Validator) checkPrivileges(result *ValidationResult) {
	check := ValidationCheck{Name: "privileges"}

	if os.Geteuid() != 0 {
		// Check if sudo is available
		if _, err := exec.LookPath("sudo"); err != nil {
			check.Passed = false
			check.Message = "Root privileges required. Run with sudo."
		} else {
			check.Passed = true
			check.Message = "Sudo available"
		}
	} else {
		check.Passed = true
		check.Message = "Running as root"
	}

	result.Checks = append(result.Checks, check)
}

func (v *Validator) checkNVIDIADriver(result *ValidationResult) {
	check := ValidationCheck{Name: "nvidia_driver"}

	// Try nvidia-smi
	cmd := exec.Command("nvidia-smi", "--query-gpu=driver_version", "--format=csv,noheader")
	output, err := cmd.Output()
	if err != nil {
		check.Passed = false
		check.Message = "NVIDIA driver not found. Please install NVIDIA drivers first."
		result.Checks = append(result.Checks, check)
		return
	}

	driverVersion := strings.TrimSpace(string(output))
	lines := strings.Split(driverVersion, "\n")
	gpuCount := len(lines)

	check.Passed = true
	check.Message = fmt.Sprintf("NVIDIA Driver: %s (%d GPU(s) detected)", lines[0], gpuCount)
	result.Checks = append(result.Checks, check)

	// Additional check for GPU details
	v.checkGPUDetails(result)
}

func (v *Validator) checkGPUDetails(result *ValidationResult) {
	check := ValidationCheck{Name: "gpu_details"}

	cmd := exec.Command("nvidia-smi", "--query-gpu=name,memory.total", "--format=csv,noheader")
	output, err := cmd.Output()
	if err != nil {
		check.Passed = true
		check.Message = "GPU details: Unable to query"
		result.Checks = append(result.Checks, check)
		return
	}

	gpus := strings.TrimSpace(string(output))
	check.Passed = true
	check.Message = fmt.Sprintf("GPUs: %s", strings.ReplaceAll(gpus, "\n", ", "))
	result.Checks = append(result.Checks, check)
}

func (v *Validator) checkNetwork(ctx context.Context, result *ValidationResult) {
	// Check master connectivity
	masterCheck := ValidationCheck{Name: "network_master"}

	client := &http.Client{Timeout: 10 * time.Second}
	healthURL := fmt.Sprintf("%s/health", v.config.MasterURL)

	req, err := http.NewRequestWithContext(ctx, "GET", healthURL, nil)
	if err != nil {
		masterCheck.Passed = false
		masterCheck.Message = fmt.Sprintf("Failed to create request: %v", err)
		result.Checks = append(result.Checks, masterCheck)
		return
	}

	resp, err := client.Do(req)
	if err != nil {
		masterCheck.Passed = false
		masterCheck.Message = fmt.Sprintf("Cannot reach master at %s: %v", v.config.MasterURL, err)
	} else {
		resp.Body.Close()
		masterCheck.Passed = true
		masterCheck.Message = fmt.Sprintf("Master reachable at %s", v.config.MasterURL)
	}

	result.Checks = append(result.Checks, masterCheck)

	// Check Redis connectivity
	redisCheck := ValidationCheck{Name: "network_redis"}
	conn, err := net.DialTimeout("tcp", v.config.RedisAddr, 5*time.Second)
	if err != nil {
		redisCheck.Passed = false
		redisCheck.Message = fmt.Sprintf("Cannot reach Redis at %s: %v", v.config.RedisAddr, err)
	} else {
		conn.Close()
		redisCheck.Passed = true
		redisCheck.Message = fmt.Sprintf("Redis reachable at %s", v.config.RedisAddr)
	}
	result.Checks = append(result.Checks, redisCheck)
}

func (v *Validator) checkPorts(result *ValidationResult) {
	check := ValidationCheck{Name: "ports"}

	requiredPorts := []int{10250, 10255} // kubelet ports
	blockedPorts := []int{}

	for _, port := range requiredPorts {
		addr := fmt.Sprintf(":%d", port)
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			blockedPorts = append(blockedPorts, port)
		} else {
			ln.Close()
		}
	}

	if len(blockedPorts) > 0 {
		check.Passed = false
		check.Message = fmt.Sprintf("Ports in use: %v. Please free these ports.", blockedPorts)
	} else {
		check.Passed = true
		check.Message = "Required ports available"
	}

	result.Checks = append(result.Checks, check)
}

func (v *Validator) checkDiskSpace(result *ValidationResult) {
	check := ValidationCheck{Name: "disk_space"}

	// Simple check using df
	cmd := exec.Command("df", "-BG", "/")
	output, err := cmd.Output()
	if err != nil {
		check.Passed = true
		check.Message = "Disk space: Unable to check"
		result.Checks = append(result.Checks, check)
		return
	}

	lines := strings.Split(string(output), "\n")
	if len(lines) >= 2 {
		fields := strings.Fields(lines[1])
		if len(fields) >= 4 {
			available := strings.TrimSuffix(fields[3], "G")
			check.Passed = true
			check.Message = fmt.Sprintf("Disk space available: %sGB", available)
		}
	}

	if check.Message == "" {
		check.Passed = true
		check.Message = "Disk space: OK"
	}

	result.Checks = append(result.Checks, check)
}

func (v *Validator) checkMemory(result *ValidationResult) {
	check := ValidationCheck{Name: "memory"}

	cmd := exec.Command("free", "-g")
	output, err := cmd.Output()
	if err != nil {
		check.Passed = true
		check.Message = "Memory: Unable to check"
		result.Checks = append(result.Checks, check)
		return
	}

	lines := strings.Split(string(output), "\n")
	if len(lines) >= 2 {
		fields := strings.Fields(lines[1])
		if len(fields) >= 2 {
			totalGB := fields[1]
			check.Passed = true
			check.Message = fmt.Sprintf("Memory: %sGB total", totalGB)
		}
	}

	if check.Message == "" {
		check.Passed = true
		check.Message = "Memory: OK"
	}

	result.Checks = append(result.Checks, check)
}

// PrintValidationResult prints the validation result in a nice format.
func PrintValidationResult(result *ValidationResult) {
	fmt.Println("\n[Validation Results]")
	fmt.Println(strings.Repeat("-", 50))

	for _, check := range result.Checks {
		status := "✓"
		if !check.Passed {
			status = "✗"
		}
		fmt.Printf("  %s %s\n", status, check.Message)
	}

	fmt.Println(strings.Repeat("-", 50))

	if len(result.Warnings) > 0 {
		fmt.Println("\nWarnings:")
		for _, w := range result.Warnings {
			fmt.Printf("  ⚠ %s\n", w)
		}
	}

	if len(result.Errors) > 0 {
		fmt.Println("\nErrors:")
		for _, e := range result.Errors {
			fmt.Printf("  ✗ %s\n", e)
		}
	}

	if result.Valid {
		fmt.Println("\n✓ All required checks passed!")
	} else {
		fmt.Println("\n✗ Some required checks failed. Please fix the errors above.")
	}
}
