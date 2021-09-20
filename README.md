# deso-rosetta

## Overview

`deso-rosetta` provides an implementation of the Rosetta API for DeSo in Golang.
If you haven't heard of the Rosetta API, you can find more
information [here](https://rosetta-api.org).

## Usage

As specified in the [Rosetta API Principles](https://www.rosetta-api.org/docs/automated_deployment.html),
all Rosetta implementations must be deployable via Docker and support running via either an
[`online` or `offline` mode](https://www.rosetta-api.org/docs/node_deployment.html#multiple-modes).

**You must install docker. You can download Docker [here](https://www.docker.com/get-started).**

### Build

Running the following commands will create a Docker image called `deso-rosetta:latest`.

1. Checkout `deso-rosetta` and `core` in the same directory

2. In the `deso-rosetta` repo, run the following (you may need sudo):

```
docker build -t deso-rosetta -f Dockerfile ..
```

### Run

You may need sudo:

```
docker run -it deso-rosetta /deso/bin/deso-rosetta run
```

Specify `--network=TESTNET --miner-public-keys=publickey` to get free testnet money. You
can easily generate a key on deso.com and copy it from your wallet page (starts with
BC).

### Testnet Example

Scripts in the `scripts/testnet` folder demonstrate how to run an online node, offline node, and construct transactions.

1. `cd scripts/testnet`
1. Start an online node using `./online.sh`
1. Start an online node using `./offline.sh`
2. Construct and submit transactions using `./send.sh`

### Rosetta-cli checks

To run checks with [`rosetta-cli`](https://github.com/coinbase/rosetta-cli), first install the rosetta-cli:

```
curl -sSfL https://raw.githubusercontent.com/coinbase/rosetta-cli/master/scripts/install.sh | sh -s
```
To run the data checks, execute:
```
bin/rosetta-cli check:data --configuration-file rosetta-cli-conf/testnet/deso.conf
```

To run the construction checks, execute:

```
bin/rosetta-cli check:construction --configuration-file rosetta-cli-conf/testnet/deso.conf
```
