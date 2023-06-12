DATA_DIR=/data
TM_CONFIG_DIR="$DATA_DIR/config"

if [ ! -d "$TM_CONFIG_DIR" ]; then
  echo "$TM_CONFIG_DIR missing, running init"
  echo "Initializing tendermint."
  mkdir -p $TM_CONFIG_DIR;
  ## initialize config
  tendermint init --home $DATA_DIR
  if [[ -n "$EMPTY_BLOCK_DELAY" ]]; then
      sed -i  's#create_empty_blocks_interval = "0s"#create_empty_blocks_interval = "5s"#g' $TM_CONFIG_DIR/config.toml
  fi
else
  echo "$TM_CONFIG_DIR exists."
fi

morphnode --sequencer --home $DATA_DIR
