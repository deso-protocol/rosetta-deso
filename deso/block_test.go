package deso

import (
	"encoding/hex"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/deso-protocol/core/lib"
	"github.com/dgraph-io/badger/v4"
	"github.com/stretchr/testify/require"
)

// This test is no longer needed, but we keep it here because it initializes a simplified node and index and might be
// useful in the future.
func TestUtxoOpsProblem(t *testing.T) {
	return
	var err error
	require := require.New(t)
	_ = require

	blockhash, err := hex.DecodeString("0000000000000a4730410273fb7db17c3a2b7f78068f1f51d6d67084a53d9ce0")
	require.NoError(err)
	dataDirectory := "/tmp/rosetta-hypersync-online-mainnet-100067"

	config := &Config{}
	config.Params = &lib.DeSoMainnetParams
	config.DataDirectory = dataDirectory
	node := NewNode(config)
	dbDir := lib.GetBadgerDbPath(dataDirectory)
	opts := lib.PerformanceBadgerOptions(dbDir)
	opts.ValueDir = dbDir
	node.chainDB, err = badger.Open(opts)
	require.NoError(err)

	rosettaIndexDir := filepath.Join(dataDirectory, "index")
	rosettaIndexOpts := lib.PerformanceBadgerOptions(rosettaIndexDir)
	rosettaIndexOpts.ValueDir = rosettaIndexDir
	rosettaIndex, err := badger.Open(rosettaIndexOpts)
	require.NoError(err)
	node.Index = NewIndex(rosettaIndex, node.chainDB)

	// Listen to transaction and block events so we can fill RosettaIndex with relevant data
	node.EventManager = lib.NewEventManager()
	node.EventManager.OnTransactionConnected(node.handleTransactionConnected)
	node.EventManager.OnBlockConnected(node.handleBlockCommitted)
	node.EventManager.OnSnapshotCompleted(node.handleSnapshotCompleted)

	minerCount := uint64(1)
	maxBlockTemplatesToCache := uint64(100)
	minBlockUpdateInterval := uint64(10)
	blockCypherAPIKey := ""
	mempoolDumpDir := ""
	disableNetworking := !node.Online
	readOnly := !node.Online
	targetOutboundPeers := uint32(8)
	maxInboundPeers := uint32(125)
	rateLimitFeerateNanosPerKB := uint64(0)
	stallTimeoutSeconds := uint64(900)

	node.Server, err, _ = lib.NewServer(
		node.Config.Params,
		node.Config.Regtest,
		nil,          // listeners
		nil,          // addrMgr
		[]string{},   // connectIPs
		node.chainDB, // db
		nil,          // postgres
		targetOutboundPeers,
		maxInboundPeers,
		[]string{}, // miner public keys
		minerCount,
		true,
		10000, // peer connection refresh interval millis
		false,
		lib.NodeSyncTypeBlockSync,
		0,
		false,
		rateLimitFeerateNanosPerKB,
		1000,
		stallTimeoutSeconds,
		maxBlockTemplatesToCache,
		minBlockUpdateInterval,
		blockCypherAPIKey,
		true,
		0,
		node.Config.DataDirectory,
		mempoolDumpDir,
		disableNetworking,
		readOnly,
		false,
		nil,
		"",
		[]string{},
		0,
		node.EventManager,
		node.nodeMessageChan,
		node.Config.ForceChecksum,
		"",
		lib.HypersyncDefaultMaxQueueSize,
		nil,        // TODO: support for rosetta as a validator?
		3000000000, // 3GB max mempool size bytes
		30000,      // 30 seconds mempool back up time millis
		1,          // 1, mempool fee estimator num mempool blocks
		50,         // 50, mempool fee estimator num past blocks
		10000,      // mempool max validation view connects
		1500,       // 1500 milliseconds, pos block production interval milliseconds
		10000,      // State syncer mempool txn sync limit
		nil,        // checkpoint syncing providers
	)
	require.NoError(err)

	blockchain := node.GetBlockchain()
	block := blockchain.GetBlock(lib.NewBlockHash(blockhash))
	if block == nil {
		t.Fatalf("Block is nil")
	}
	utxoOpsForBlock, _ := node.Index.GetUtxoOps(block)
	fmt.Printf("Retrieved the utxoops, length: (%v)\n", len(utxoOpsForBlock))

	txns := node.GetTransactionsForConvertBlock(block)
	_ = txns
	fmt.Printf("Retrieved transactions, length: (%v)\n", len(txns))
}
