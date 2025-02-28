package main

import (
	"encoding/hex"
	"fmt"
	"github.com/deso-protocol/core/lib"
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

	msgDesoTxn := lib.MsgDeSoTxn{}
	err = msgDesoTxn.FromBytes(transactionHex)
	if err != nil {
		panic(err)
	}
	sig, err := msgDesoTxn.Sign(privateKey)
	if err != nil {
		panic(err)
	}
	rVal := sig.R()
	sVal := sig.S()
	rBytes := (&rVal).Bytes()
	sBytes := (&sVal).Bytes()
	r := hex.EncodeToString(rBytes[:])
	s := hex.EncodeToString(sBytes[:])

	fmt.Printf("%s%s\n", r, s)
}
