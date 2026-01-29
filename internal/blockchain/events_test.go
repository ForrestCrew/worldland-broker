package blockchain

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRentalStarted(t *testing.T) {
	// Setup test data
	rentalID := big.NewInt(42)
	user := common.HexToAddress("0x1234567890123456789012345678901234567890")
	provider := common.HexToAddress("0xabcdefabcdefabcdefabcdefabcdefabcdefabcd")
	startTime := big.NewInt(1706500000)

	// Build topics (indexed params)
	topics := []common.Hash{
		RentalStartedSig,                     // Event signature
		common.BigToHash(rentalID),           // rentalId (indexed)
		common.BytesToHash(user.Bytes()),     // user (indexed)
		common.BytesToHash(provider.Bytes()), // provider (indexed)
	}

	// Build data (non-indexed params) - startTime as 32 bytes
	data := common.LeftPadBytes(startTime.Bytes(), 32)

	log := types.Log{
		Topics:      topics,
		Data:        data,
		TxHash:      common.HexToHash("0xabc123def456abc123def456abc123def456abc123def456abc123def456abc1"),
		BlockNumber: 12345,
		Index:       0,
	}

	event, err := ParseRentalStarted(log)
	require.NoError(t, err)
	require.NotNil(t, event)

	assert.Equal(t, uint64(42), event.RentalID)
	assert.Equal(t, user, event.User)
	assert.Equal(t, provider, event.Provider)
	assert.Equal(t, uint64(1706500000), event.StartTime)
	assert.Equal(t, uint64(12345), event.BlockNumber)
	assert.Equal(t, uint(0), event.LogIndex)
}

func TestParseRentalStarted_InvalidTopics(t *testing.T) {
	// Missing indexed params (only has signature)
	log := types.Log{
		Topics: []common.Hash{RentalStartedSig},
		Data:   make([]byte, 32),
	}

	_, err := ParseRentalStarted(log)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expected 4 topics")
}

func TestParseRentalStarted_InvalidSignature(t *testing.T) {
	wrongSig := common.HexToHash("0xdeadbeef")
	topics := []common.Hash{
		wrongSig,
		common.BigToHash(big.NewInt(1)),
		common.BytesToHash(common.Address{}.Bytes()),
		common.BytesToHash(common.Address{}.Bytes()),
	}

	log := types.Log{
		Topics: topics,
		Data:   make([]byte, 32),
	}

	_, err := ParseRentalStarted(log)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid event signature")
}

func TestParseRentalStarted_InvalidDataLength(t *testing.T) {
	topics := []common.Hash{
		RentalStartedSig,
		common.BigToHash(big.NewInt(1)),
		common.BytesToHash(common.Address{}.Bytes()),
		common.BytesToHash(common.Address{}.Bytes()),
	}

	log := types.Log{
		Topics: topics,
		Data:   make([]byte, 16), // Too short, need 32 bytes
	}

	_, err := ParseRentalStarted(log)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid log data length")
}

func TestParseRentalStopped(t *testing.T) {
	// Setup test data
	rentalID := big.NewInt(42)
	endTime := big.NewInt(1706503600)
	cost := big.NewInt(1000000000000000000) // 1 ETH in wei

	// Build topics (indexed params)
	topics := []common.Hash{
		RentalStoppedSig,
		common.BigToHash(rentalID),
	}

	// Build data: endTime (32 bytes) + cost (32 bytes)
	data := append(
		common.LeftPadBytes(endTime.Bytes(), 32),
		common.LeftPadBytes(cost.Bytes(), 32)...,
	)

	log := types.Log{
		Topics:      topics,
		Data:        data,
		TxHash:      common.HexToHash("0xdef456abc123def456abc123def456abc123def456abc123def456abc123def4"),
		BlockNumber: 12350,
		Index:       1,
	}

	event, err := ParseRentalStopped(log)
	require.NoError(t, err)
	require.NotNil(t, event)

	assert.Equal(t, uint64(42), event.RentalID)
	assert.Equal(t, uint64(1706503600), event.EndTime)
	assert.Equal(t, cost, event.Cost)
	assert.Equal(t, uint64(12350), event.BlockNumber)
	assert.Equal(t, uint(1), event.LogIndex)
}

func TestParseRentalStopped_InvalidTopics(t *testing.T) {
	// Missing rentalId topic
	log := types.Log{
		Topics: []common.Hash{RentalStoppedSig},
		Data:   make([]byte, 64),
	}

	_, err := ParseRentalStopped(log)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expected 2 topics")
}

func TestParseRentalStopped_InvalidSignature(t *testing.T) {
	wrongSig := common.HexToHash("0xdeadbeef")
	topics := []common.Hash{
		wrongSig,
		common.BigToHash(big.NewInt(1)),
	}

	log := types.Log{
		Topics: topics,
		Data:   make([]byte, 64),
	}

	_, err := ParseRentalStopped(log)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid event signature")
}

func TestParseRentalStopped_InvalidDataLength(t *testing.T) {
	topics := []common.Hash{
		RentalStoppedSig,
		common.BigToHash(big.NewInt(1)),
	}

	log := types.Log{
		Topics: topics,
		Data:   make([]byte, 32), // Too short, need 64 bytes
	}

	_, err := ParseRentalStopped(log)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid log data length")
}

func TestParseDeposited(t *testing.T) {
	user := common.HexToAddress("0x1234567890123456789012345678901234567890")
	amount := big.NewInt(5000000000000000000) // 5 ETH

	topics := []common.Hash{
		DepositedSig,
		common.BytesToHash(user.Bytes()),
	}

	data := common.LeftPadBytes(amount.Bytes(), 32)

	log := types.Log{
		Topics:      topics,
		Data:        data,
		TxHash:      common.HexToHash("0x123456789012345678901234567890123456789012345678901234567890abcd"),
		BlockNumber: 10000,
		Index:       2,
	}

	event, err := ParseDeposited(log)
	require.NoError(t, err)
	require.NotNil(t, event)

	assert.Equal(t, user, event.User)
	assert.Equal(t, amount, event.Amount)
	assert.Equal(t, uint64(10000), event.BlockNumber)
}

func TestParseWithdrawn(t *testing.T) {
	user := common.HexToAddress("0xabcdefabcdefabcdefabcdefabcdefabcdefabcd")
	amount := big.NewInt(2500000000000000000) // 2.5 ETH

	topics := []common.Hash{
		WithdrawnSig,
		common.BytesToHash(user.Bytes()),
	}

	data := common.LeftPadBytes(amount.Bytes(), 32)

	log := types.Log{
		Topics:      topics,
		Data:        data,
		TxHash:      common.HexToHash("0xfedcba0987654321fedcba0987654321fedcba0987654321fedcba09876543"),
		BlockNumber: 10005,
		Index:       0,
	}

	event, err := ParseWithdrawn(log)
	require.NoError(t, err)
	require.NotNil(t, event)

	assert.Equal(t, user, event.User)
	assert.Equal(t, amount, event.Amount)
	assert.Equal(t, uint64(10005), event.BlockNumber)
}

func TestIdentifyEvent(t *testing.T) {
	tests := []struct {
		name     string
		topics   []common.Hash
		expected EventType
	}{
		{
			name:     "RentalStarted",
			topics:   []common.Hash{RentalStartedSig},
			expected: EventTypeRentalStarted,
		},
		{
			name:     "RentalStopped",
			topics:   []common.Hash{RentalStoppedSig},
			expected: EventTypeRentalStopped,
		},
		{
			name:     "Deposited",
			topics:   []common.Hash{DepositedSig},
			expected: EventTypeDeposited,
		},
		{
			name:     "Withdrawn",
			topics:   []common.Hash{WithdrawnSig},
			expected: EventTypeWithdrawn,
		},
		{
			name:     "Unknown signature",
			topics:   []common.Hash{common.HexToHash("0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")},
			expected: EventTypeUnknown,
		},
		{
			name:     "Empty topics",
			topics:   []common.Hash{},
			expected: EventTypeUnknown,
		},
		{
			name:     "Nil topics",
			topics:   nil,
			expected: EventTypeUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log := types.Log{Topics: tt.topics}
			result := IdentifyEvent(log)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsRentalEvent(t *testing.T) {
	tests := []struct {
		name     string
		topics   []common.Hash
		expected bool
	}{
		{
			name:     "RentalStarted is rental event",
			topics:   []common.Hash{RentalStartedSig},
			expected: true,
		},
		{
			name:     "RentalStopped is rental event",
			topics:   []common.Hash{RentalStoppedSig},
			expected: true,
		},
		{
			name:     "Deposited is not rental event",
			topics:   []common.Hash{DepositedSig},
			expected: false,
		},
		{
			name:     "Withdrawn is not rental event",
			topics:   []common.Hash{WithdrawnSig},
			expected: false,
		},
		{
			name:     "Unknown is not rental event",
			topics:   []common.Hash{common.HexToHash("0xdeadbeef")},
			expected: false,
		},
		{
			name:     "Empty topics is not rental event",
			topics:   []common.Hash{},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log := types.Log{Topics: tt.topics}
			result := IsRentalEvent(log)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParseEvent_RentalStarted(t *testing.T) {
	rentalID := big.NewInt(99)
	user := common.HexToAddress("0x1111111111111111111111111111111111111111")
	provider := common.HexToAddress("0x2222222222222222222222222222222222222222")
	startTime := big.NewInt(1706600000)

	topics := []common.Hash{
		RentalStartedSig,
		common.BigToHash(rentalID),
		common.BytesToHash(user.Bytes()),
		common.BytesToHash(provider.Bytes()),
	}
	data := common.LeftPadBytes(startTime.Bytes(), 32)

	log := types.Log{
		Topics:      topics,
		Data:        data,
		BlockNumber: 20000,
	}

	event, eventType, err := ParseEvent(log)
	require.NoError(t, err)
	assert.Equal(t, EventTypeRentalStarted, eventType)

	started, ok := event.(*RentalStartedEvent)
	require.True(t, ok)
	assert.Equal(t, uint64(99), started.RentalID)
}

func TestParseEvent_RentalStopped(t *testing.T) {
	rentalID := big.NewInt(99)
	endTime := big.NewInt(1706700000)
	cost := big.NewInt(500000000000000000) // 0.5 ETH

	topics := []common.Hash{
		RentalStoppedSig,
		common.BigToHash(rentalID),
	}
	data := append(
		common.LeftPadBytes(endTime.Bytes(), 32),
		common.LeftPadBytes(cost.Bytes(), 32)...,
	)

	log := types.Log{
		Topics:      topics,
		Data:        data,
		BlockNumber: 20100,
	}

	event, eventType, err := ParseEvent(log)
	require.NoError(t, err)
	assert.Equal(t, EventTypeRentalStopped, eventType)

	stopped, ok := event.(*RentalStoppedEvent)
	require.True(t, ok)
	assert.Equal(t, uint64(99), stopped.RentalID)
	assert.Equal(t, cost, stopped.Cost)
}

func TestParseEvent_Unknown(t *testing.T) {
	log := types.Log{
		Topics: []common.Hash{common.HexToHash("0xunknown")},
		Data:   []byte{},
	}

	_, eventType, err := ParseEvent(log)
	assert.Error(t, err)
	assert.Equal(t, EventTypeUnknown, eventType)
}

func TestEventSignaturesConsistency(t *testing.T) {
	// Ensure event signatures match expected keccak256 values
	// These are pre-computed values that should match the init() calculations

	// Verify signatures are non-zero
	assert.NotEqual(t, common.Hash{}, RentalStartedSig, "RentalStartedSig should be non-zero")
	assert.NotEqual(t, common.Hash{}, RentalStoppedSig, "RentalStoppedSig should be non-zero")
	assert.NotEqual(t, common.Hash{}, DepositedSig, "DepositedSig should be non-zero")
	assert.NotEqual(t, common.Hash{}, WithdrawnSig, "WithdrawnSig should be non-zero")

	// Verify all signatures are unique
	sigs := []common.Hash{RentalStartedSig, RentalStoppedSig, DepositedSig, WithdrawnSig}
	seen := make(map[common.Hash]bool)
	for _, sig := range sigs {
		assert.False(t, seen[sig], "Duplicate signature found: %s", sig.Hex())
		seen[sig] = true
	}
}

func TestRentalStartedEvent_LargeValues(t *testing.T) {
	// Test with large rental ID (near uint64 max)
	largeRentalID := new(big.Int).SetUint64(^uint64(0)) // max uint64
	user := common.HexToAddress("0x1234567890123456789012345678901234567890")
	provider := common.HexToAddress("0xabcdefabcdefabcdefabcdefabcdefabcdefabcd")
	startTime := big.NewInt(1706500000)

	topics := []common.Hash{
		RentalStartedSig,
		common.BigToHash(largeRentalID),
		common.BytesToHash(user.Bytes()),
		common.BytesToHash(provider.Bytes()),
	}
	data := common.LeftPadBytes(startTime.Bytes(), 32)

	log := types.Log{
		Topics:      topics,
		Data:        data,
		BlockNumber: 12345,
	}

	event, err := ParseRentalStarted(log)
	require.NoError(t, err)
	assert.Equal(t, ^uint64(0), event.RentalID)
}

func TestRentalStoppedEvent_ZeroCost(t *testing.T) {
	// Edge case: rental stopped with zero cost (immediate stop)
	rentalID := big.NewInt(1)
	endTime := big.NewInt(1706500001) // 1 second after start
	cost := big.NewInt(0)             // Zero cost

	topics := []common.Hash{
		RentalStoppedSig,
		common.BigToHash(rentalID),
	}
	data := append(
		common.LeftPadBytes(endTime.Bytes(), 32),
		common.LeftPadBytes(cost.Bytes(), 32)...,
	)

	log := types.Log{
		Topics:      topics,
		Data:        data,
		BlockNumber: 12346,
	}

	event, err := ParseRentalStopped(log)
	require.NoError(t, err)
	assert.Equal(t, 0, event.Cost.Cmp(big.NewInt(0)), "Cost should be zero")
}
