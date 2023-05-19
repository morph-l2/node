DATA_DIR=/data
TM_CHAINDATA_DIR="$DATA_DIR/tendermint"
# GENESIS_FILE_PATH="${GENESIS_FILE_PATH:-/genesis.json}"

if [ ! -d "$TM_CHAINDATA_DIR" ]; then
  echo "$TM_CHAINDATA_DIR missing, running init"
  echo "Initializing tendermint."
  mkdir -p $TM_CHAINDATA_DIR;
  tendermint init --home $TM_CHAINDATA_DIR
else
  echo "$TM_CHAINDATA_DIR exists."
fi

morphnode --sequencer --home $DATA_DIR
