package services

import (
	"fmt"
	"github.com/coinbase/rosetta-sdk-go/types"
	"github.com/deso-protocol/core"
	"strconv"
	"strings"
)

func ParseCoinIdentifier(coinIdentifier *types.CoinIdentifier) (*core.BlockHash, uint32, error) {
	utxoSpent := strings.Split(coinIdentifier.Identifier, ":")

	hash := core.MustDecodeHexBlockHash(utxoSpent[0])

	outpointIndex, err := strconv.ParseUint(utxoSpent[1], 10, 32)
	if err != nil {
		return nil, 0, fmt.Errorf("%w unable to parse outpoint_index", err)
	}

	return hash, uint32(outpointIndex), nil
}
