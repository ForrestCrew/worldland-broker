package monitoring

// QuotaStatus represents ResourceQuota hard limits vs current usage
type QuotaStatus struct {
	GPUHard    int    `json:"gpuHard"`    // Max GPUs allowed
	GPUUsed    int    `json:"gpuUsed"`    // Currently allocated GPUs
	CPUHard    string `json:"cpuHard"`    // e.g., "32" (cores)
	CPUUsed    string `json:"cpuUsed"`    // e.g., "8" (cores)
	MemoryHard string `json:"memoryHard"` // e.g., "128Gi"
	MemoryUsed string `json:"memoryUsed"` // e.g., "32Gi"
	PodsHard   int    `json:"podsHard"`   // Max pods allowed
	PodsUsed   int    `json:"podsUsed"`   // Currently running pods
}

// ResourceMetrics represents real-time resource utilization
type ResourceMetrics struct {
	CPUMilliCores int64 `json:"cpuMilliCores"` // Real-time CPU usage (1 core = 1000)
	MemoryMiB     int64 `json:"memoryMiB"`     // Real-time memory in MiB
}

// TenantUsage represents a tenant's quota and real-time usage
type TenantUsage struct {
	Namespace     string          `json:"namespace"`
	UserAddress   string          `json:"userAddress"`
	Quota         QuotaStatus     `json:"quota"`         // What's allocated (quota limits vs used)
	RealTimeUsage ResourceMetrics `json:"realTimeUsage"` // What's actually being consumed
	ActivePods    int             `json:"activePods"`    // Running GPU pods count
}

// SessionMetrics represents a single session's resource usage
type SessionMetrics struct {
	SessionID     string `json:"sessionId"`
	PodName       string `json:"podName"`
	Namespace     string `json:"namespace"`
	ProviderID    string `json:"providerId"`
	PodPhase      string `json:"podPhase"` // Pending, Running, Failed, Succeeded
	IsReady       bool   `json:"isReady"`  // Readiness probe passed
	CPUMilliCores int64  `json:"cpuMilliCores"`
	MemoryMiB     int64  `json:"memoryMiB"`
	GPUCount      int    `json:"gpuCount"`               // Requested GPU count
	ExpiresAt     string `json:"expiresAt,omitempty"`    // From annotation
}

// ProviderStats represents provider dashboard data
type ProviderStats struct {
	ProviderID       string           `json:"providerId"`
	TotalSessions    int              `json:"totalSessions"`    // All sessions (any state)
	ActiveSessions   int              `json:"activeSessions"`   // Running + Ready
	TotalCPUUsage    int64            `json:"totalCpuUsage"`    // Aggregate CPU (milliCores)
	TotalMemoryUsage int64            `json:"totalMemoryUsage"` // Aggregate memory (MiB)
	Sessions         []SessionMetrics `json:"sessions"`
}

// ClusterStats represents cluster-wide statistics (admin view)
type ClusterStats struct {
	TotalNodes          int         `json:"totalNodes"`
	TotalGPUs           int         `json:"totalGpus"`
	TotalActiveSessions int         `json:"totalActiveSessions"`
	NodeMetrics         []NodeStats `json:"nodeMetrics,omitempty"`
}

// NodeStats represents a single node's resource status
type NodeStats struct {
	NodeName      string `json:"nodeName"`
	CPUMilliCores int64  `json:"cpuMilliCores"`
	MemoryMiB     int64  `json:"memoryMiB"`
	GPUCount      int    `json:"gpuCount"` // Labeled GPU count
}
