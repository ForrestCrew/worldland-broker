package k8s

import (
	"testing"
)

func TestGetManager_Singleton(t *testing.T) {
	// Call GetManager multiple times
	mgr1 := GetManager()
	mgr2 := GetManager()
	mgr3 := GetManager()

	// All should return the same instance
	if mgr1 != mgr2 {
		t.Error("GetManager() returned different instance on second call")
	}

	if mgr1 != mgr3 {
		t.Error("GetManager() returned different instance on third call")
	}

	if mgr2 != mgr3 {
		t.Error("GetManager() second and third calls returned different instances")
	}
}

func TestClientsetManager_IsInitialized(t *testing.T) {
	// Note: We can't fully test initialization without a real K8s cluster or mock
	// This test only verifies the IsInitialized method works correctly

	// Create a new manager (not using singleton to avoid test pollution)
	mgr := &ClientsetManager{}

	// Should not be initialized initially
	if mgr.IsInitialized() {
		t.Error("ClientsetManager should not be initialized initially")
	}
}

func TestClientsetManager_GetClientset_NotInitialized(t *testing.T) {
	// Create a new manager (not using singleton to avoid test pollution)
	mgr := &ClientsetManager{}

	// Should return error when not initialized
	_, err := mgr.GetClientset()
	if err == nil {
		t.Error("GetClientset() should return error when not initialized")
	}

	expectedErrMsg := "clientset not initialized"
	if err.Error() != expectedErrMsg {
		t.Errorf("GetClientset() error = %v, want %v", err.Error(), expectedErrMsg)
	}
}

func TestClientsetManager_GetConfig_NotInitialized(t *testing.T) {
	// Create a new manager (not using singleton to avoid test pollution)
	mgr := &ClientsetManager{}

	// Should return nil when not initialized
	config := mgr.GetConfig()
	if config != nil {
		t.Error("GetConfig() should return nil when not initialized")
	}
}
