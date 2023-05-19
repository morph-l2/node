#!/bin/bash

export MORPH_NODE_L2_ETH_RPC=http://127.0.0.1:9545
export MORPH_NODE_L2_ENGINE_RPC=http://127.0.0.1:9551
export MORPH_NODE_L2_ENGINE_AUTH=ops-morphism/jwt-secret.txt
export MORPH_NODE_L1_ETH_RPC=https://arb1.arbitrum.io/rpc
export MORPH_NODE_SYNC_DEPOSIT_CONTRACT_ADDRESS=0xFd086bC7CD5C481DCC9C85ebE478A1C0b69FCbb9
## export MORPH_NODE_SYNC_START_HEIGHT=88854536

./build/bin/morphnode --sequencer --home build