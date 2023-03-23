#!/bin/bash
scripts/reset.sh

test_mnemonic="wage thunder live sense resemble foil apple course spin horse glass mansion midnight laundry acoustic rhythm loan scale talent push green direct brick please"

pstaked init test --chain-id test-core-1
echo $test_mnemonic | pstaked keys add test --recover --keyring-backend test
pstaked add-genesis-account test 100000000000000stake,100000000000000uxprt --keyring-backend test
pstaked gentx test 10000000stake --chain-id test-core-1 --keyring-backend test
pstaked collect-gentxs

# Actually a cross-platform solution, sed is rubbish
# Example usage: $REGEX_REPLACE 's/^param = ".*?"/param = "100"/' config.toml
REGEX_REPLACE="perl -i -pe"

$REGEX_REPLACE 's|timeout_commit = "5s"|timeout_commit = "1s"|g' ~/.pstaked/config/config.toml
$REGEX_REPLACE 's|timeout_propose = "3s"|timeout_propose = "1s"|g' ~/.pstaked/config/config.toml
$REGEX_REPLACE 's|index_all_keys = false|index_all_keys = true|g' ~/.pstaked/config/config.toml
