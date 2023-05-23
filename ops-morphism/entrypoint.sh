DATA_DIR=/data
TM_CONFIG_DIR="$DATA_DIR/config"

if [ ! -d "$TM_CONFIG_DIR" ]; then
  echo "$TM_CONFIG_DIR missing, running init"
  echo "Initializing tendermint."
  mkdir -p $TM_CONFIG_DIR;
  tendermint init --home $DATA_DIR
else
  echo "$TM_CONFIG_DIR exists."
fi

morphnode --sequencer --home $DATA_DIR
