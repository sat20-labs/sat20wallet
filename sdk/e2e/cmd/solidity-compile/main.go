package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/e2e"
)

func main() {
	contract := flag.String("contract", "", "contract name to extract from the Solidity source")
	solc := flag.String("solc", "", "path to solc; defaults to PATH lookup")
	evmVersion := flag.String("evm-version", "", "solc EVM version; defaults to the e2e compiler default")
	timeout := flag.Duration("timeout", 30*time.Second, "solc execution timeout")
	height := flag.Uint64("height", 0, "block height used for gas-price calculation")
	inputGas := flag.Int64("input-gas", 0, "optional input gas asset amount; when set, the gas plan also reports change")
	deployGasLimit := flag.Int64("deploy-gas-limit", 0, "optional EVM deploy gas limit to calculate deploy gas funding")
	invokeGasLimit := flag.Int64("invoke-gas-limit", 0, "optional EVM invoke gas limit to calculate invoke gas funding")
	invokeNeedsResult := flag.Bool("invoke-needs-result", false, "include result base gas in the invoke gas funding plan")
	flag.Parse()

	if flag.NArg() != 1 {
		fmt.Fprintf(os.Stderr, "usage: go run ./e2e/cmd/solidity-compile --contract Counter path/to/Contract.sol\n")
		os.Exit(2)
	}

	artifact, err := e2e.CompileSolidityFile(flag.Arg(0), *contract, e2e.SolidityCompileOptions{
		SolcPath:   *solc,
		EVMVersion: *evmVersion,
		Timeout:    *timeout,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	output := struct {
		e2e.SolidityArtifact
		DeployGasPlan *e2e.EVMGasPlan `json:"deployGasPlan,omitempty"`
		InvokeGasPlan *e2e.EVMGasPlan `json:"invokeGasPlan,omitempty"`
	}{
		SolidityArtifact: artifact,
	}
	if *deployGasLimit != 0 {
		plan, err := e2e.EVMDeployGasPlan(*deployGasLimit, *height, *inputGas)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		output.DeployGasPlan = &plan
	}
	if *invokeGasLimit != 0 {
		plan, err := e2e.EVMInvokeGasPlan(*invokeGasLimit, *height, *invokeNeedsResult, *inputGas)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		output.InvokeGasPlan = &plan
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(output); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
