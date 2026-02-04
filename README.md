# rosetta-deso

## Overview

`rosetta-deso` provides an implementation of the Rosetta API for DeSo in Golang.
If you haven't heard of the Rosetta API, you can find more
information [here](https://rosetta-api.org).

## Usage

As specified in the [Rosetta API Principles](https://www.rosetta-api.org/docs/automated_deployment.html),
all Rosetta implementations must be deployable via Docker and support running via either an
[`online` or `offline` mode](https://www.rosetta-api.org/docs/node_deployment.html#multiple-modes).

**You must install docker. You can download Docker [here](https://www.docker.com/get-started).**

### Build

Running the following commands will create a Docker image called `rosetta-deso:latest`.

1. Checkout `rosetta-deso` and `core` in the same directory

2. In the `rosetta-deso` repo, run the following (you may need sudo):

```
docker build -t rosetta-deso -f Dockerfile .
```

### Run

You may need sudo:

```
docker run -p 17005:17005 -it rosetta-deso /deso/bin/rosetta-deso run
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

### M1 Mac Users

There are some issues with running the rosetta cli w/ M1 Macs. To fix this, you can run the following command to build
the rosetta cli docker image:
```
docker build github.com/coinbase/mesh-cli --platform linux/amd64 -t rosetta-cli
```

Then update the configuration file to use `http://host.docker.internal:17005` instead of `http://localhost:17005` -
basically replacing all usages of `localhost` with `host.docker.internal`.

Then to run the rosetta cli check:data tests, you can run the following command:
```
docker run -v "$(pwd):/data" --rm -it --platform linux/amd64 rosetta-cli check:data --configuration-file /data/rosetta-cli-conf/testnet/deso.conf
```