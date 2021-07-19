package main

import (
	"encoding/hex"
	"fmt"
	"github.com/bitclout/core/lib"
	"os"
)

func main() {
	seedHex, err := hex.DecodeString(os.Args[1])
	if err != nil {
		panic(err)
	}

	transactionHex, err := hex.DecodeString(os.Args[2])
	if err != nil {
		panic(err)
	}

	_, privateKey, _, err := lib.ComputeKeysFromSeedWithNet(seedHex, 0, true)
	if err != nil {
		panic(err)
	}

	signature, _ := privateKey.Sign(transactionHex)
	r := hex.EncodeToString(signature.R.Bytes())
	s := hex.EncodeToString(signature.S.Bytes())

	fmt.Printf("%s%s\n", r, s)
}
