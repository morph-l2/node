#!/bin/bash

export L2_NODE_L2_ETH_RPC=http://127.0.0.1:8545
export L2_NODE_L2_ENGINE_RPC=http://127.0.0.1:8551
export L2_NODE_L2_ENGINE_AUTH=jwt-secret.txt
export L2_NODE_L1_ETH_RPC=https://arb1.arbitrum.io/rpc
export L2_NODE_SYNC_DEPOSIT_CONTRACT_ADDRESS=0xFd086bC7CD5C481DCC9C85ebE478A1C0b69FCbb9
export SYNC_START_HEIGHT=88854536

./build/l2node --sequencer