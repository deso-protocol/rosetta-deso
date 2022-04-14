package deso

import (
	"encoding/hex"
	"fmt"
	"github.com/deso-protocol/core/lib"
	"github.com/dgraph-io/badger/v3"
	"github.com/stretchr/testify/require"
	"path/filepath"
	"testing"
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
	node.Index = NewIndex(rosettaIndex)

	// Listen to transaction and block events so we can fill RosettaIndex with relevant data
	node.EventManager = lib.NewEventManager()
	node.EventManager.OnTransactionConnected(node.handleTransactionConnected)
	node.EventManager.OnBlockConnected(node.handleBlockConnected)
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
		nil,
		nil,
		[]string{},
		node.chainDB,
		nil,
		targetOutboundPeers,
		maxInboundPeers,
		[]string{},
		minerCount,
		true,
		false,
		false,
		0,
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
		nil,
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
