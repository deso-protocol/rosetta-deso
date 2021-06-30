#!/usr/bin/env bash

# NOTE: This script serves as a proof-of-concept of how all the online and offline endpoints work.
# For now, it uses a single, hardcoded public key to mine and send money.

# The account used to mine and send coins is:
# - Seed: escape gown wasp foam super claw eager foot opera hold roast cement
# - Seed Hex: 623751da8e50c314de0e79e7857e0bba067b7ebf5cb853535da5cad2360a678e
# - Testnet public key base58: tBCKWCLyYKGa4Lb4buEsMGYePEWcaVAqcunvDVD4zVDcH8NoB5EgPF
# - Testnet public key bytes: 02b872c1b6acd3be581a27e53a15a2c9d095d3204dad3c2d7fe9a31f8c077126fe

# Online node is running on port 17005
ONLINE=http://localhost:17005

# Offline node is running on port 17006
OFFLINE=http://localhost:17006

COIN=$(curl -s -X POST -d '@data/coins.json' $ONLINE/account/coins | jq '.coins[0].coin_identifier.identifier')
echo -e "Sending coin $COIN\n"

TXN=$(jq ".operations[0].coin_change.coin_identifier.identifier = $COIN" data/txn.json)
PREPROCESS=$(echo $TXN | curl -s -X POST --data-binary @- $OFFLINE/construction/preprocess | jq '.options')
echo -e "Preprocess options:\n$PREPROCESS\n"

# TODO: Include the PREPROCESS options result in this call
METADATA=$(curl -s -X POST -d '@data/metadata.json' $ONLINE/construction/metadata | jq)
echo -e "Metadata result:\n$METADATA\n"

# TODO: Use the suggested_fee from METADATA to adjust the inputs accordingly
PAYLOADS=$(echo $TXN | curl -s -X POST --data-binary @- $OFFLINE/construction/payloads | jq)
UNSIGNED_TXN=$(echo $PAYLOADS | jq '.unsigned_transaction')
UNSIGNED_BYTES=$(echo $PAYLOADS | jq '.payloads[0].hex_bytes')
echo -e "Payloads unsigned bytes:\n$UNSIGNED_BYTES\n"

# TODO: Verify $TXN matches $PARSE_UNSIGNED
PARSE_UNSIGNED=$(jq ".transaction = $UNSIGNED_TXN" data/parse_unsigned.json | curl -s -X POST --data-binary @- $OFFLINE/construction/parse | jq)
echo -e "Parse unsigned result:\n$PARSE_UNSIGNED\n"

SIGNATURE=$(go run sign.go 623751da8e50c314de0e79e7857e0bba067b7ebf5cb853535da5cad2360a678e $UNSIGNED_BYTES)
echo -e "Transaction signature:\n$SIGNATURE\n"

COMBINE_DATA=$(jq ".unsigned_transaction = $UNSIGNED_TXN | .signatures[0].hex_bytes = \"$SIGNATURE\"" data/combine.json)
COMBINE=$(echo $COMBINE_DATA | curl -s -X POST --data-binary @- $OFFLINE/construction/combine | jq '.signed_transaction')
echo -e "Combine result:\n$COMBINE\n"

HASH=$(jq ".signed_transaction = $COMBINE" data/submit.json | curl -s -X POST --data-binary @- $OFFLINE/construction/hash | jq)
echo -e "Hash result:\n$HASH\n"

# FIXME: For some reason I'm getting an invalid signature when I shouldn't be
SUBMIT=$(jq ".signed_transaction = $COMBINE" data/submit.json | curl -s -X POST --data-binary @- $ONLINE/construction/submit | jq)
echo -e "Submit result:\n$SUBMIT\n"
