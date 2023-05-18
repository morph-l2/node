package flags

import "github.com/urfave/cli"

const envVarPrefix = "MORPH_NODE_"

func prefixEnvVar(name string) string {
	return envVarPrefix + name
}

var (
	Home = cli.StringFlag{
		Name:     "home",
		Usage:    "home directory for morph-node",
		EnvVar:   prefixEnvVar("HOME"),
		Required: false,
	}

	L2EthAddr = cli.StringFlag{
		Name:     "l2.eth",
		Usage:    "Address of L2 Engine JSON-RPC endpoints to use (eth namespace required)",
		EnvVar:   prefixEnvVar("L2_ETH_RPC"),
		Required: true,
	}

	L2EngineAddr = cli.StringFlag{
		Name:     "l2.engine",
		Usage:    "Address of L2 Engine JSON-RPC endpoints to use (engine namespace required)",
		EnvVar:   prefixEnvVar("L2_ENGINE_RPC"),
		Required: true,
	}

	L2EngineJWTSecret = cli.StringFlag{
		Name:        "l2.jwt-secret",
		Usage:       "Path to JWT secret key. Keys are 32 bytes, hex encoded in a file. A new key will be generated if left empty.",
		EnvVar:      prefixEnvVar("L2_ENGINE_AUTH"),
		Value:       "",
		Destination: new(string),
	}

	MaxL1MessageNumPerBlock = cli.Uint64Flag{
		Name:   "maxL1MessageNumPerBlock",
		Usage:  "The max number allowed for L1 message type transactions to involve in one block",
		EnvVar: prefixEnvVar("MAX_L1_MESSAGE_NUM_PER_BLOCK"),
	}

	L2CrossDomainMessengerContractAddr = cli.StringFlag{
		Name:   "l2CDMContractAddr",
		Usage:  "L2CrossDomainMessenger contract address",
		EnvVar: prefixEnvVar("L2_CDM_CONTRACT_ADDRESS"),
	}

	L1NodeAddr = cli.StringFlag{
		Name:   "l1.rpc",
		Usage:  "Address of L1 User JSON-RPC endpoint to use (eth namespace required)",
		EnvVar: prefixEnvVar("L1_ETH_RPC"),
	}

	L1Confirmations = cli.Int64Flag{
		Name:   "l1.confirmations",
		Usage:  "Number of confirmations on L1 needed for finalization",
		EnvVar: prefixEnvVar("L1_CONFIRMATIONS"),
	}

	// Flags for syncer
	SyncDepositContractAddr = cli.StringFlag{
		Name:   "sync.depositContractAddr",
		Usage:  "Contract address deployed on layer one",
		EnvVar: prefixEnvVar("SYNC_DEPOSIT_CONTRACT_ADDRESS"),
	}

	SyncStartHeight = cli.Uint64Flag{
		Name:   "sync.startHeight",
		Usage:  "Block height where syncer start to fetch",
		EnvVar: prefixEnvVar("SYNC_START_HEIGHT"),
	}

	SyncPollInterval = cli.DurationFlag{
		Name:   "sync.pollInterval",
		Usage:  "Frequency at which we query for new L1 messages",
		EnvVar: prefixEnvVar("SYNC_POLL_INTERVAL"),
	}

	SyncLogProgressInterval = cli.DurationFlag{
		Name:   "sync.logProgressInterval",
		Usage:  "frequency at which we log progress",
		EnvVar: prefixEnvVar("SYNC_LOG_PROGRESS_INTERVAL"),
	}

	SyncFetchBlockRange = cli.Uint64Flag{
		Name:   "sync.fetchBlockRange",
		Usage:  "Number of blocks that we collect in a single eth_getLogs query",
		EnvVar: prefixEnvVar("SYNC_FETCH_BLOCK_RANGE"),
	}

	// db options
	DBDataDir = cli.StringFlag{
		Name:   "db.dir",
		Usage:  "Directory of the data",
		EnvVar: prefixEnvVar("DB_DIR"),
	}

	DBNamespace = cli.StringFlag{
		Name:   "db.namespace",
		Usage:  "Database namespace",
		EnvVar: prefixEnvVar("DB_NAMESPACE"),
	}

	DBHandles = cli.IntFlag{
		Name:   "db.handles",
		Usage:  "Database handles",
		EnvVar: prefixEnvVar("DB_HANDLES"),
	}

	DBCache = cli.IntFlag{
		Name:   "db.cache",
		Usage:  "Database cache",
		EnvVar: prefixEnvVar("DB_CACHE"),
	}

	DBFreezer = cli.StringFlag{
		Name:   "db.freezer",
		Usage:  "Database freezer",
		EnvVar: prefixEnvVar("DB_FREEZER"),
	}

	SequencerEnabled = &cli.BoolFlag{
		Name:   "sequencer",
		Usage:  "Enable the sequencer mode",
		EnvVar: prefixEnvVar("SEQUENCER"),
	}

	TendermintConfigPath = &cli.StringFlag{
		Name:   "tdm.config",
		Usage:  "Directory of tendermint config file",
		EnvVar: prefixEnvVar("TDM_CONFIG"),
	}

	MockEnabled = &cli.BoolFlag{
		Name:   "mock",
		Usage:  "Enable mock; If enabled, we start a simulated sequencer to manage the block production, just for dev/test use",
		EnvVar: prefixEnvVar("MOCK_SEQUENCER"),
	}
)

var Flags = []cli.Flag{
	Home,
	L1NodeAddr,
	L1Confirmations,
	L2EthAddr,
	L2EngineAddr,
	L2EngineJWTSecret,
	MaxL1MessageNumPerBlock,
	L2CrossDomainMessengerContractAddr,

	// sync optioins
	SyncDepositContractAddr,
	SyncStartHeight,
	SyncPollInterval,
	SyncLogProgressInterval,
	SyncFetchBlockRange,

	// db options
	DBDataDir,
	DBNamespace,
	DBHandles,
	DBCache,
	DBFreezer,

	SequencerEnabled,
	TendermintConfigPath,
	MockEnabled,
}
