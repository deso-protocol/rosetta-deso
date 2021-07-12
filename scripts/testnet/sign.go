package main

import (
	"encoding/hex"
	"fmt"
	"github.com/bitclout/core/lib"
	"github.com/golang/glog"
	"os"
)

func main() {
	seedHex, _ := hex.DecodeString(os.Args[1])
	transactionHex, _ := hex.DecodeString(os.Args[2])
	pubKey, privateKey, _, _ := lib.ComputeKeysFromSeedWithNet(seedHex, 0, true)
	signature, _ := privateKey.Sign(transactionHex)

	glog.Info(signature)
	glog.Info(lib.PkToStringTestnet(pubKey.SerializeCompressed()))

	r := hex.EncodeToString(signature.R.Bytes())
	s := hex.EncodeToString(signature.S.Bytes())
	fmt.Printf("%s%s\n", r, s)
}
