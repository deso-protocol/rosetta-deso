# rosetta-bitclout

## Overview

`rosetta-bitclout` provides an implementation of the Rosetta API for BitClout in Golang.
If you haven't heard of the Rosetta API, you can find more
information [here](https://rosetta-api.org).

## Usage

As specified in the [Rosetta API Principles](https://www.rosetta-api.org/docs/automated_deployment.html),
all Rosetta implementations must be deployable via Docker and support running via either an
[`online` or `offline` mode](https://www.rosetta-api.org/docs/node_deployment.html#multiple-modes).

**You must install docker. You can download Docker [here](https://www.docker.com/get-started).**

### Build

Running the following commands will create a Docker image called `rosetta-bitclout:latest`.

1. Checkout `rosetta-bitclout` and `core` in the same directory

2. In the `rosetta-bitclout` repo, run the following (you may need sudo):

```
docker build -t rosetta-bitclout -f Dockerfile ..
```

### Run

You may need sudo:

```
docker run -it rosetta-bitclout /bitclout/bin/rosetta-bitclout run
```

Specify `--network=TESTNET --miner-public-keys=publickey` to get free testnet money. You
can easily generate a key on bitclout.com and copy it from your wallet page (starts with
BC).
