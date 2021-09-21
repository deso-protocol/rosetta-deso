package services

import (
	"fmt"
	"github.com/coinbase/rosetta-sdk-go/types"
	"github.com/deso-protocol/core/lib"
	"strconv"
	"strings"
)

func ParseCoinIdentifier(coinIdentifier *types.CoinIdentifier) (*lib.BlockHash, uint32, error) {
	utxoSpent := strings.Split(coinIdentifier.Identifier, ":")

	hash := lib.MustDecodeHexBlockHash(utxoSpent[0])

	outpointIndex, err := strconv.ParseUint(utxoSpent[1], 10, 32)
	if err != nil {
		return nil, 0, fmt.Errorf("%w unable to parse outpoint_index", err)
	}

	return hash, uint32(outpointIndex), nil
}
