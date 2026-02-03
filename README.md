# Worldland Hub

**Go backend server for GPU rental marketplace coordination**

The Hub manages provider registration, user sessions, rental matching, and communicates with GPU Nodes via mTLS.

## Quick Start (5 minutes)

```bash
# Start PostgreSQL
docker-compose up -d postgres

# Wait for healthy status (check with: docker-compose ps)

# Install Go dependencies
go mod download

# Build and run Hub
go build -o hub ./cmd/hub
./hub
```

Verify: `curl http://localhost:8080/health` should return `{"status":"ok"}`

## Prerequisites

**Required:**
- Go 1.24.0 or higher ([download](https://go.dev/dl/))
- Docker and Docker Compose ([download](https://docs.docker.com/get-docker/))

**Check versions:**
```bash
go version          # Should be go1.24.0 or higher
docker --version
docker-compose --version
```

**Optional (for production):**
- PostgreSQL 16+ (if not using Docker)

## Installation

### 1. Clone and navigate

```bash
cd worldland-hub
```

### 2. Install Go dependencies

```bash
go mod download
```

This downloads all Go packages defined in go.mod.

### 3. Start PostgreSQL

Using docker-compose (recommended for development):

```bash
docker-compose up -d postgres
```

Verify PostgreSQL is running:
```bash
docker-compose ps
# postgres should show "Up" and "healthy"
```

**Expected output:**
```
NAME                    COMMAND                  SERVICE     STATUS          PORTS
worldland-hub-postgres-1   "docker-entrypoint.s..."   postgres    Up (healthy)    0.0.0.0:5432->5432/tcp
```

### 4. Build Hub

```bash
go build -o hub ./cmd/hub
```

This creates the `hub` binary in the current directory.

## Configuration

### Environment Variables

Create a `.env` file or export environment variables. See `.env.example` for all options.

**Database (defaults work with docker-compose):**
| Variable | Default | Description |
|----------|---------|-------------|
| DB_HOST | localhost | PostgreSQL host |
| DB_PORT | 5432 | PostgreSQL port |
| DB_USER | worldland | Database user |
| DB_PASSWORD | devpassword | Database password |
| DB_NAME | worldland_hub | Database name |

**Server:**
| Variable | Default | Description |
|----------|---------|-------------|
| SERVER_PORT | 8080 | HTTP API port |
| MTLS_PORT | 8443 | mTLS port (Node connections) |

**Authentication:**
| Variable | Default | Description |
|----------|---------|-------------|
| SIWE_DOMAIN | hub.worldland.io | Domain for SIWE authentication |

**Blockchain (for event listener):**
| Variable | Default | Description |
|----------|---------|-------------|
| CONTRACT_ADDRESS | (none) | Deployed WorldlandRental contract |
| BLOCKCHAIN_RPC_ENDPOINTS | wss://bsc-ws-node.nariox.org:443 | WebSocket RPC (comma-separated) |
| BLOCKCHAIN_HTTP_RPC | https://bsc-dataseed1.binance.org | HTTP RPC for balance queries |
| BLOCKCHAIN_LISTENER_ENABLED | true | Enable/disable event listener |
| CONTRACT_DEPLOYMENT_BLOCK | 0 | Block number for initial backfill |

**mTLS Certificates:**
| Variable | Default | Description |
|----------|---------|-------------|
| CA_CERT_PATH | ./dev/step-ca/certs/root_ca.crt | CA certificate |
| CA_KEY_PATH | ./dev/step-ca/secrets/root_ca_key | CA private key |
| MTLS_CERT_PATH | ./dev/certs/hub.crt | Hub server certificate |
| MTLS_KEY_PATH | ./dev/certs/hub.key | Hub server private key |
| NODE_CLIENT_CERT_PATH | certs/hub-client.crt | Hub client cert (for Node connections) |
| NODE_CLIENT_KEY_PATH | certs/hub-client.key | Hub client key (for Node connections) |

**ACME (Automatic Certificate):**
| Variable | Default | Description |
|----------|---------|-------------|
| ACME_DIRECTORY_URL | https://localhost:9000/acme/acme/directory | step-ca ACME endpoint |

## Running Locally

### Development Mode (no mTLS)

```bash
# Ensure PostgreSQL is running
docker-compose up -d postgres

# Run Hub (uses default config)
./hub
```

Hub will start on:
- HTTP API: http://localhost:8080
- mTLS: https://localhost:8443 (requires certificates)

**Verify:**
```bash
curl http://localhost:8080/health
# Expected: {"status":"ok"}
```

### With step-ca (mTLS enabled)

```bash
# Start PostgreSQL and step-ca
docker-compose up -d

# Wait for both services
docker-compose ps
# postgres and step-ca should show "healthy"

# Run Hub
./hub
```

### Using environment file

```bash
# Create .env from template
cp .env.example .env

# Edit as needed
nano .env

# Run with env file (bash)
export $(cat .env | xargs) && ./hub
```

## Testing

### Run all tests

```bash
go test ./...
```

### With verbose output

```bash
go test -v ./...
```

### Specific package

```bash
go test -v ./internal/auth/...
go test -v ./internal/rental/...
go test -v ./internal/blockchain/...
```

### Integration tests

```bash
go test -v ./test/integration/...
```

### E2E Tests

Full E2E tests are located in the `../e2e/` directory and test the complete rental flow with real blockchain interactions.

```bash
cd ../e2e
make up        # Start infrastructure (PostgreSQL + Hardhat + Hub)
npm test       # Run E2E tests
make down      # Stop infrastructure
```

## E2E Test Coverage (v1.4)

### K8s E2E Path (Phase 25) ✅

| Component | Test Coverage | Description |
|-----------|---------------|-------------|
| **SIWE Authentication** | ✅ Complete | Wallet signature verification, session token issuance |
| **Node Registration** | ✅ Complete | Provider registers GPU node via Hub API |
| **mTLS Certificate Issuance** | ✅ Complete | Hub issues client certificate, node activation |
| **Token Deposit** | ✅ Complete | Real ERC20 deposit to WorldlandRental contract |
| **Balance Validation** | ✅ Complete | Hub verifies on-chain balance before session creation |
| **Provider Discovery** | ✅ Complete | Search available GPU nodes by criteria |
| **Session Creation** | ✅ Complete | Create rental session with balance check |
| **Blockchain Rental Start** | ✅ Complete | `startRental()` transaction, event emission |
| **Blockchain Rental Stop** | ✅ Complete | `stopRental()` transaction, state verification |
| **PostgreSQL Integration** | ✅ Complete | All CRUD operations (providers, nodes, sessions) |
| **K8s Pod Provisioning** | ✅ Complete | Pod created in tenant namespace with proper labels |
| **K8s Tenant Isolation** | ✅ Complete | Namespace per user, ResourceQuota, NetworkPolicy |
| **SSH Connection Info** | ✅ Complete | Host, port, password returned from Hub API |
| **Settlement Verification** | ✅ Complete | Settlement data available after terminate |

### Node E2E Path (Phase 26-27) ✅

| Component | Test Coverage | Description |
|-----------|---------------|-------------|
| **Node mTLS Auto-Registration** | ✅ Complete | Node auto-registers using certificate CN |
| **Node Discovery** | ✅ Complete | Renter discovers registered nodes via API |
| **2-User Rental Flow** | ✅ Complete | Provider + Renter full lifecycle |
| **Blockchain Integration** | ✅ Complete | Real deposit, startRental, stopRental transactions |

### Not Yet Tested ❌

| Component | Status | Reason |
|-----------|--------|--------|
| **Actual SSH Connection** | ❌ | SSH-enabled container image not deployed |
| **GPU Container Execution** | ❌ | Requires actual GPU hardware |
| **Session Extension** | ❌ | `extend` API not called in current tests |
| **Session Timeout** | ❌ | Time-based expiration not tested in E2E |
| **K8s + Node Integration** | ❌ | Combined K8s tenant + Node provisioning not tested |

### Test Architecture

```
┌─────────────────────────────────────────────────────────────┐
│  E2E Test Coverage (v1.4)                                   │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  ┌── K8s Path (Phase 25) ────────────────────────────────┐  │
│  │                                                       │  │
│  │  [Renter] ──SIWE──→ [Hub] ──SQL──→ [PostgreSQL]  ✅  │  │
│  │     │                  │                              │  │
│  │     │                  ├──K8s API──→ [Kind Cluster]   │  │
│  │     │                  │                │             │  │
│  │     │                  │            [K8s Pod] ✅      │  │
│  │     │                  │                │             │  │
│  │     │                  │          [SSH Info] ✅       │  │
│  │     │                  │                              │  │
│  │     └──eth_call──→ [Hardhat Blockchain]          ✅  │  │
│  └───────────────────────────────────────────────────────┘  │
│                                                             │
│  ┌── Node Path (Phase 26-27) ────────────────────────────┐  │
│  │                                                       │  │
│  │  [Provider] ──mTLS──→ [Hub] ←── [worldland-node] ✅  │  │
│  │     │                    │                            │  │
│  │     │                    ├──SQL──→ [PostgreSQL]  ✅  │  │
│  │     │                    │                            │  │
│  │  [Renter] ──SIWE──→ [Hub] ──Discover Nodes──→  ✅    │  │
│  │     │                                                 │  │
│  │     └──eth_call──→ [Hardhat Blockchain]          ✅  │  │
│  └───────────────────────────────────────────────────────┘  │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

### Running E2E Tests

#### K8s E2E (Phase 25)
```bash
cd ../e2e
make up                    # Start Hub + PostgreSQL + Hardhat + Kind cluster
make test                  # Run K8s E2E tests
make down                  # Stop all services
```

#### Node E2E (Phase 26-27)
```bash
cd ../e2e
make -f Makefile.node up   # Start Hub + PostgreSQL + Hardhat + worldland-node
NODE_E2E=true make -f Makefile.node test  # Run Node E2E tests
make -f Makefile.node down # Stop all services
```

#### Go E2E Tests (from worldland-hub)
```bash
# K8s E2E
USE_EXISTING_CLUSTER=true go test -tags=e2e -v ./test/e2e/... -run TestCompleteRentalFlowE2E

# Node E2E (requires docker-compose.node.yml running)
NODE_E2E=true go test -tags=e2e -v ./test/e2e/... -run TestNodeBased2UserRentalFlow
```

## API Endpoints

### Public Endpoints

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | /health | Health check |
| GET | /api/v1/auth/nonce | Get SIWE nonce |
| POST | /api/v1/auth/login | Verify SIWE signature |
| GET | /api/v1/ca/root | Get CA root certificate |

### Protected Endpoints (require SIWE auth)

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | /api/v1/auth/logout | End session |
| POST | /api/v1/nodes | Register node |
| GET | /api/v1/nodes | List provider's nodes |
| GET | /api/v1/nodes/:id | Get node details |
| PATCH | /api/v1/nodes/:id/price | Update node price |
| POST | /api/v1/nodes/:id/certificate | Issue mTLS certificate |

### Rental Endpoints (require SIWE auth)

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | /api/v1/rentals/providers | Search available providers |
| POST | /api/v1/rentals | Create rental session |
| GET | /api/v1/rentals | List user's sessions |
| DELETE | /api/v1/rentals/:id | Cancel session |
| POST | /api/v1/rentals/:id/start | Start rental |
| POST | /api/v1/rentals/:id/stop | Stop rental |
| GET | /api/v1/balance | Get user balance |

### History Endpoints (public)

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | /api/history/deposits-withdraws/:address | Deposit/withdraw history |
| GET | /api/history/rentals/:address | Rental history |

## Project Structure

```
worldland-hub/
├── cmd/
│   ├── hub/              # Hub server binary
│   └── indexer/          # Blockchain event indexer (standalone)
├── internal/
│   ├── adapters/         # External adapters
│   │   ├── http/         # HTTP handlers and router
│   │   ├── mtls/         # mTLS server for Node connections
│   │   └── postgres/     # PostgreSQL repositories
│   ├── auth/             # SIWE authentication
│   ├── blockchain/       # Blockchain event listener
│   ├── config/           # Configuration loading
│   ├── domain/           # Domain models and interfaces
│   ├── indexer/          # Event indexer query repository
│   ├── matching/         # Provider-user matching
│   ├── middleware/       # Auth middleware
│   ├── rental/           # Rental business logic
│   ├── services/         # Service orchestration
│   ├── sessions/         # Rental session management
│   └── settlement/       # Settlement calculation
├── migrations/           # SQL migrations (auto-run on startup)
├── test/
│   └── integration/      # Integration tests
├── docker-compose.yml    # Local infrastructure
├── go.mod                # Go dependencies
└── .env.example          # Environment template
```

## Troubleshooting

### Error: "Failed to connect to database"

**Cause:** PostgreSQL not running or wrong credentials.

**Solution:**
```bash
# Check if PostgreSQL is running
docker-compose ps

# If not running, start it
docker-compose up -d postgres

# Check logs for errors
docker-compose logs postgres

# Verify connection manually
psql -h localhost -U worldland -d worldland_hub
# Password: devpassword
```

### Error: "address already in use :8080"

**Cause:** Another process using port 8080.

**Solution:**
```bash
# Find process using port
lsof -i :8080

# Kill it (replace PID)
kill -9 <PID>

# Or use different port
SERVER_PORT=8081 ./hub
```

### Error: "SIWE verification failed"

**Cause:** Domain mismatch between frontend and Hub.

**Solution:**
Ensure `SIWE_DOMAIN` matches what the frontend uses for signing. Default is `hub.worldland.io`.

For local development, frontend should use the same domain in SIWE message.

### PostgreSQL connection issues

```bash
# Test connection manually
psql -h localhost -U worldland -d worldland_hub
# Password: devpassword

# Reset database (WARNING: deletes all data)
docker-compose down -v
docker-compose up -d postgres
```

### mTLS certificate errors

**Cause:** Missing or expired certificates.

**Solution for development:**
Hub automatically generates self-signed certificates when files don't exist. Check logs for:
```
CA cert not found, generating self-signed CA for development
```

For production, ensure all certificate paths are correctly configured.

### Blockchain listener not starting

**Cause:** Missing contract address or RPC configuration.

**Check logs for:**
```
Event listener disabled enabled=true hasContract=false
```

**Solution:**
Set `CONTRACT_ADDRESS` to the deployed WorldlandRental contract address.

### Health check fails

```bash
# Check if Hub is running
ps aux | grep hub

# Check logs
./hub 2>&1 | head -50

# Verify port binding
netstat -tlnp | grep 8080
```

## Development

### Hot reload (using air)

```bash
# Install air
go install github.com/air-verse/air@latest

# Run with hot reload
air
```

### Database migrations

Migrations run automatically on Hub startup. To run manually:

```bash
# Migrations are in ./migrations/
# Applied in order by filename
```

### Generating test coverage

```bash
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## License

MIT
