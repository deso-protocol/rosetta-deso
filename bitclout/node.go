package bitclout

import (
	"fmt"
	"math/rand"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/bitclout/core/lib"
	"github.com/btcsuite/btcd/addrmgr"
	"github.com/btcsuite/btcd/wire"
	"github.com/davecgh/go-spew/spew"
	"github.com/dgraph-io/badger/v3"
	"github.com/golang/glog"
	"github.com/sasha-s/go-deadlock"
)

func getAddrsToListenOn(protocolPort int) ([]net.TCPAddr, []net.Listener) {
	listeningAddrs := []net.TCPAddr{}
	listeners := []net.Listener{}
	ifaceAddrs, err := net.InterfaceAddrs()
	if err != nil {
		// TODO: Error handling
	} else {
		for _, iAddr := range ifaceAddrs {
			ifaceIP, _, err := net.ParseCIDR(iAddr.String())
			if err != nil {
				// TODO: Error handling
				continue
			}

			if ifaceIP.IsLinkLocalUnicast() {
				// TODO: Error handling
				continue
			}

			netAddr := net.TCPAddr{
				IP:   ifaceIP,
				Port: int(protocolPort),
			}

			listener, err := net.Listen(netAddr.Network(), netAddr.String())
			if err != nil {
				// TODO: Error handling
				continue
			}

			listeners = append(listeners, listener)
			listeningAddrs = append(listeningAddrs, netAddr)
		}
	}

	// TODO: Error handling
	return listeningAddrs, listeners
}

func addIPsForHost(bitcloutAddrMgr *addrmgr.AddrManager, host string, params *lib.BitCloutParams) {
	ipAddrs, err := net.LookupIP(host)
	if err != nil {
		glog.Tracef("_addSeedAddrs: DNS discovery failed on seed host (continuing on): %s %v\n", host, err)
		return
	}
	if len(ipAddrs) == 0 {
		glog.Tracef("_addSeedAddrs: No IPs found for host: %s\n", host)
		return
	}

	// Don't take more than 5 IPs per host.
	ipsPerHost := 5
	if len(ipAddrs) > ipsPerHost {
		glog.Debugf("_addSeedAddrs: Truncating IPs found from %d to %d\n", len(ipAddrs), ipsPerHost)
		ipAddrs = ipAddrs[:ipsPerHost]
	}

	glog.Debugf("_addSeedAddrs: Adding seed IPs from seed %s: %v\n", host, ipAddrs)

	// Convert addresses to NetAddress'es.
	netAddrs := make([]*wire.NetAddress, len(ipAddrs))
	for ii, ip := range ipAddrs {
		netAddrs[ii] = wire.NewNetAddressTimestamp(
			// We initialize addresses with a
			// randomly selected "last seen time" between 3
			// and 7 days ago similar to what bitcoind does.
			time.Now().Add(-1*time.Second*time.Duration(lib.SecondsIn3Days+
				lib.RandInt32(lib.SecondsIn4Days))),
			0,
			ip,
			params.DefaultSocketPort)
	}
	glog.Debugf("_addSeedAddrs: Computed the following wire.NetAddress'es: %s", spew.Sdump(netAddrs))

	// Normally the second argument is the source who told us about the
	// addresses we're adding. In this case since the source is a DNS seed
	// just use the first address in the fetch as the source.
	bitcloutAddrMgr.AddAddresses(netAddrs, netAddrs[0])
}

func addSeedAddrsFromPrefixes(bitcloutAddrMgr *addrmgr.AddrManager, params *lib.BitCloutParams) {
	MaxIterations := 99999

	// This one iterates sequentially.
	go func() {
		for dnsNumber := 0; dnsNumber < MaxIterations; dnsNumber++ {
			var wg deadlock.WaitGroup
			for _, dnsGeneratorOuter := range params.DNSSeedGenerators {
				wg.Add(1)
				go func(dnsGenerator []string) {
					dnsString := fmt.Sprintf("%s%d%s", dnsGenerator[0], dnsNumber, dnsGenerator[1])
					glog.Tracef("_addSeedAddrsFromPrefixes: Querying DNS seed: %s", dnsString)
					addIPsForHost(bitcloutAddrMgr, dnsString, params)
					wg.Done()
				}(dnsGeneratorOuter)
			}
			wg.Wait()
		}
	}()

	// This one iterates randomly.
	go func() {
		for index := 0; index < MaxIterations; index++ {
			dnsNumber := int(rand.Int63() % int64(MaxIterations))
			var wg deadlock.WaitGroup
			for _, dnsGeneratorOuter := range params.DNSSeedGenerators {
				wg.Add(1)
				go func(dnsGenerator []string) {
					dnsString := fmt.Sprintf("%s%d%s", dnsGenerator[0], dnsNumber, dnsGenerator[1])
					glog.Tracef("_addSeedAddrsFromPrefixes: Querying DNS seed: %s", dnsString)
					addIPsForHost(bitcloutAddrMgr, dnsString, params)
					wg.Done()
				}(dnsGeneratorOuter)
			}
			wg.Wait()
		}
	}()
}

type Node struct {
	*lib.Server
	DataDirectory   string
	Online          bool
	Params          *lib.BitCloutParams
	Port            int
	MinerPublicKeys []string
}

func NewNode(online bool, dataDirectory string, params *lib.BitCloutParams, port int, minerPublicKeys []string) *Node {
	result := Node{}
	result.DataDirectory = dataDirectory
	result.Online = online
	result.Params = params
	result.Port = port
	result.MinerPublicKeys = minerPublicKeys

	return &result
}

func (node *Node) Start() {
	bitcloutAddrMgr := addrmgr.New(node.DataDirectory, net.LookupIP)
	bitcloutAddrMgr.Start()

	listeningAddrs, listeners := getAddrsToListenOn(node.Port)

	if node.Online {
		for _, addr := range listeningAddrs {
			netAddr := wire.NewNetAddress(&addr, 0)
			bitcloutAddrMgr.AddLocalAddress(netAddr, addrmgr.BoundPrio)
		}

		for _, host := range node.Params.DNSSeeds {
			addIPsForHost(bitcloutAddrMgr, host, node.Params)
		}

		go addSeedAddrsFromPrefixes(bitcloutAddrMgr, node.Params)
	}

	bitcoinDataDir := filepath.Join(node.DataDirectory, "bitcoin_manager")
	if err := os.MkdirAll(bitcoinDataDir, os.ModePerm); err != nil {
		fmt.Errorf("Could not create Bitcoin datadir (%s): %v", node.DataDirectory, err)

		panic(err)
	}

	dbDir := lib.GetBadgerDbPath(node.DataDirectory)
	opts := badger.DefaultOptions(dbDir)
	opts.ValueDir = dbDir
	opts.MemTableSize = 1024 << 20
	db, err := badger.Open(opts)
	if err != nil {
		panic(err)
	}

	// Note: This is one of many seeds. We specify it explicitly for convenience,
	// but not specifying it would make the code run just the same.
	connectIPAddrs := []string{"bitclout-seed-4.io"}
	minerCount := uint64(1)
	maxBlockTemplatesToCache := uint64(100)
	minBlockUpdateInterval := uint64(10)
	blockCypherAPIKey := ""
	mempoolDumpDir := ""
	disableNetworking := !node.Online
	readOnly := !node.Online
	ignoreInboundPeerInvMessages := false
	bitcoinConnectPeer := ""
	ignoreUnminedBitcoinTxns := false
	targetOutboundPeers := uint32(8)
	maxInboundPeers := uint32(125)
	limitOneInboundConnectionPerIP := true
	rateLimitFeerateNanosPerKB := uint64(0)
	minFeeRateNanosPerKB := uint64(1000)
	stallTimeoutSeconds := uint64(900)

	node.Server, err = lib.NewServer(
		node.Params,
		listeners,
		bitcloutAddrMgr,
		connectIPAddrs,
		db,
		targetOutboundPeers,
		maxInboundPeers,
		node.MinerPublicKeys,
		minerCount,
		limitOneInboundConnectionPerIP,
		rateLimitFeerateNanosPerKB,
		minFeeRateNanosPerKB,
		stallTimeoutSeconds,
		bitcoinDataDir,
		maxBlockTemplatesToCache,
		minBlockUpdateInterval,
		blockCypherAPIKey,
		true,
		node.DataDirectory,
		mempoolDumpDir,
		disableNetworking,
		readOnly,
		ignoreInboundPeerInvMessages,
		bitcoinConnectPeer,
		ignoreUnminedBitcoinTxns,
		nil,
		"",
		[]string{},
		0,
	)
	if err != nil {
		panic(err)
	}

	node.Server.Start()
}
