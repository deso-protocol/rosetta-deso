package deso

import (
	"errors"
	"github.com/golang/glog"
	"os"
	"path/filepath"
	"strings"

	"github.com/coinbase/rosetta-sdk-go/types"
	"github.com/deso-protocol/core/lib"
	"github.com/spf13/viper"
)

type Config struct {
	Mode                   Mode
	Network                *types.NetworkIdentifier
	Params                 *lib.DeSoParams
	Currency               *types.Currency
	GenesisBlockIdentifier *types.BlockIdentifier
	Port                   int
	NodePort               int
	DataDirectory          string
	MinerPublicKeys        []string
	Regtest                bool
	ConnectIPs             []string
	HyperSync              bool
	DisableSlowSync        bool
	MaxSyncBlockHeight     uint32
	ArchivalMode           bool

	// Glog flags
	LogDirectory string
	GlogV        uint64
	GlogVmodule  string
}

func LoadConfig() (*Config, error) {
	result := Config{}

	switch result.Mode = Mode(strings.ToUpper(viper.GetString("mode"))); result.Mode {
	case Online, Offline:
	default:
		return nil, errors.New("unknown mode")
	}

	result.Currency = &Currency

	switch network := Network(strings.ToUpper(viper.GetString("network"))); network {
	case Mainnet:
		result.Params = &lib.DeSoMainnetParams
	case Testnet:
		result.Params = &lib.DeSoTestnetParams
	default:
		return nil, errors.New("unknown network")
	}

	result.Network = &types.NetworkIdentifier{
		Blockchain: "DeSo",
		Network:    result.Params.NetworkType.String(),
	}

	result.GenesisBlockIdentifier = &types.BlockIdentifier{
		Hash: result.Params.GenesisBlockHashHex,
	}

	dataDir := viper.GetString("data-directory")
	result.DataDirectory = filepath.Join(dataDir, lib.DBVersionString)
	if err := os.MkdirAll(result.DataDirectory, os.ModePerm); err != nil {
		glog.Fatalf("Could not create data directories (%s): %v", result.DataDirectory, err)
	}
	result.Port = viper.GetInt("port")
	result.NodePort = viper.GetInt("node-port")
	result.MinerPublicKeys = viper.GetStringSlice("miner-public-keys")
	result.Regtest = viper.GetBool("regtest")
	result.ConnectIPs = viper.GetStringSlice("connect-ips")

	result.HyperSync = viper.GetBool("hypersync")

	// Glog flags
	result.LogDirectory = viper.GetString("log-dir")
	if result.LogDirectory == "" {
		result.LogDirectory = result.DataDirectory
	}
	result.GlogV = viper.GetUint64("glog-v")
	result.GlogVmodule = viper.GetString("glog-vmodule")

	result.DisableSlowSync = viper.GetBool("disable-slow-sync")
	result.MaxSyncBlockHeight = viper.GetUint32("max-sync-block-height")
	result.ArchivalMode = viper.GetBool("archival-mode")

	return &result, nil
}
