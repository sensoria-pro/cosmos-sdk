package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/client/tx"
	kmultisig "github.com/cosmos/cosmos-sdk/crypto/keys/multisig"

	authclient "github.com/cosmos/cosmos-sdk/x/auth/client"
)

const (
	flagMultisig        = "multisig"
	flagOverwrite       = "overwrite"
	flagSigOnly         = "signature-only"
	flagAmino           = "amino"
	flagNoAutoIncrement = "no-auto-increment"
)

// GetSignBatchCommand returns the transaction sign-batch command.
func GetSignBatchCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sign-batch [file]",
		Short: "Sign transaction batch files",
		Long: `Sign batch files of transactions generated with --generate-only.
The command processes list of transactions from file (one StdTx each line), generate
signed transactions or signatures and print their JSON encoding, delimited by '\n'.
As the signatures are generated, the command updates the account and sequence number accordingly.

If the --signature-only flag is set, it will output the signature parts only.

The --offline flag makes sure that the client will not reach out to full node.
As a result, the account and the sequence number queries will not be performed and
it is required to set such parameters manually. Note, invalid values will cause
the transaction to fail. The sequence will be incremented automatically for each
transaction that is signed.

If --account-number or --sequence flag is used when offline=false, they are ignored and 
overwritten by the default flag values.

The --multisig=<multisig_key> flag generates a signature on behalf of a multisig
account key. It implies --signature-only.
`,
		PreRun: preSignCmd,
		RunE:   makeSignBatchCmd(),
		Args:   cobra.ExactArgs(1),
	}

	cmd.Flags().String(flagMultisig, "", "Address or key name of the multisig account on behalf of which the transaction shall be signed")
	cmd.Flags().String(flags.FlagOutputDocument, "", "The document will be written to the given file instead of STDOUT")
	cmd.Flags().Bool(flagSigOnly, true, "Print only the generated signature, then exit")
	flags.AddTxFlagsToCmd(cmd)

	cmd.MarkFlagRequired(flags.FlagFrom)

	return cmd
}

func makeSignBatchCmd() func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		clientCtx, err := client.GetClientTxContext(cmd)
		if err != nil {
			return err
		}
		txFactory := tx.NewFactoryCLI(clientCtx, cmd.Flags())
		txCfg := clientCtx.TxConfig
		printSignatureOnly, _ := cmd.Flags().GetBool(flagSigOnly)
		infile := os.Stdin

		ms, err := cmd.Flags().GetString(flagMultisig)
		if err != nil {
			return err
		}

		// prepare output document
		closeFunc, err := setOutputFile(cmd)
		if err != nil {
			return err
		}

		defer closeFunc()
		clientCtx.WithOutput(cmd.OutOrStdout())

		if args[0] != "-" {
			infile, err = os.Open(args[0])
			if err != nil {
				return err
			}
		}
		scanner := authclient.NewBatchScanner(txCfg, infile)

		if !clientCtx.Offline {
			if ms == "" {
				from, err := cmd.Flags().GetString(flags.FlagFrom)
				if err != nil {
					return err
				}

				addr, _, _, err := client.GetFromFields(clientCtx, txFactory.Keybase(), from)
				if err != nil {
					return err
				}

				acc, err := txFactory.AccountRetriever().GetAccount(clientCtx, addr)
				if err != nil {
					return err
				}

				txFactory = txFactory.WithAccountNumber(acc.GetAccountNumber()).WithSequence(acc.GetSequence())
			} else {
				txFactory = txFactory.WithAccountNumber(0).WithSequence(0)
			}
		}

		for sequence := txFactory.Sequence(); scanner.Scan(); sequence++ {
			unsignedStdTx := scanner.Tx()
			txFactory = txFactory.WithSequence(sequence)
			txBuilder, err := txCfg.WrapTxBuilder(unsignedStdTx)
			if err != nil {
				return err
			}
			if ms == "" {
				from, _ := cmd.Flags().GetString(flags.FlagFrom)
				_, fromName, _, err := client.GetFromFields(clientCtx, txFactory.Keybase(), from)
				if err != nil {
					return fmt.Errorf("error getting account from keybase: %w", err)
				}
				err = authclient.SignTx(txFactory, clientCtx, fromName, txBuilder, true, true)
				if err != nil {
					return err
				}
			} else {
				multisigAddr, _, _, err := client.GetFromFields(clientCtx, txFactory.Keybase(), ms)
				if err != nil {
					return fmt.Errorf("error getting account from keybase: %w", err)
				}
				err = authclient.SignTxWithSignerAddress(
					txFactory, clientCtx, multisigAddr, clientCtx.GetFromName(), txBuilder, clientCtx.Offline, true)
				if err != nil {
					return err
				}
			}

			if err != nil {
				return err
			}

			json, err := marshalSignatureJSON(txCfg, txBuilder, printSignatureOnly)
			if err != nil {
				return err
			}

			cmd.Printf("%s\n", json)
		}

		if err := scanner.UnmarshalErr(); err != nil {
			return err
		}

		return scanner.UnmarshalErr()
	}
}

func setOutputFile(cmd *cobra.Command) (func(), error) {
	outputDoc, _ := cmd.Flags().GetString(flags.FlagOutputDocument)
	if outputDoc == "" {
		return func() {}, nil
	}

	fp, err := os.OpenFile(outputDoc, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return func() {}, err
	}

	cmd.SetOut(fp)

	return func() { fp.Close() }, nil
}

// GetSignCommand returns the transaction sign command.
func GetSignCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sign [file]",
		Short: "Sign a transaction generated offline",
		Long: `Sign a transaction created with the --generate-only flag.
It will read a transaction from [file], sign it, and print its JSON encoding.

If the --signature-only flag is set, it will output the signature parts only.

The --offline flag makes sure that the client will not reach out to full node.
As a result, the account and sequence number queries will not be performed and
it is required to set such parameters manually. Note, invalid values will cause
the transaction to fail.

The --multisig=<multisig_key> flag generates a signature on behalf of a multisig account
key. It implies --signature-only. Full multisig signed transactions may eventually
be generated via the 'multisign' command.
`,
		PreRun: preSignCmd,
		RunE:   makeSignCmd(),
		Args:   cobra.ExactArgs(1),
	}

	cmd.Flags().String(flagMultisig, "", "Address or key name of the multisig account on behalf of which the transaction shall be signed")
	cmd.Flags().Bool(flagOverwrite, false, "Overwrite existing signatures with a new one. If disabled, new signature will be appended")
	cmd.Flags().Bool(flagSigOnly, false, "Print only the signatures")
	cmd.Flags().String(flags.FlagOutputDocument, "", "The document will be written to the given file instead of STDOUT")
	cmd.Flags().Bool(flagAmino, false, "Generate Amino encoded JSON suitable for submiting to the txs REST endpoint")
	flags.AddTxFlagsToCmd(cmd)

	cmd.MarkFlagRequired(flags.FlagFrom)

	return cmd
}

func preSignCmd(cmd *cobra.Command, _ []string) {
	// Conditionally mark the account and sequence numbers required as no RPC
	// query will be done.
	if offline, _ := cmd.Flags().GetBool(flags.FlagOffline); offline {
		cmd.MarkFlagRequired(flags.FlagAccountNumber)
		cmd.MarkFlagRequired(flags.FlagSequence)
	}
}

func makeSignCmd() func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) (err error) {
		var clientCtx client.Context

		clientCtx, err = client.GetClientTxContext(cmd)
		if err != nil {
			return err
		}
		f := cmd.Flags()

		clientCtx, txF, newTx, err := readTxAndInitContexts(clientCtx, cmd, args[0])
		if err != nil {
			return err
		}

		txCfg := clientCtx.TxConfig
		txBuilder, err := txCfg.WrapTxBuilder(newTx)
		if err != nil {
			return err
		}

		printSignatureOnly, _ := cmd.Flags().GetBool(flagSigOnly)
		multisig, _ := cmd.Flags().GetString(flagMultisig)
		if err != nil {
			return err
		}
		from, _ := cmd.Flags().GetString(flags.FlagFrom)
		_, fromName, _, err := client.GetFromFields(clientCtx, txF.Keybase(), from)
		if err != nil {
			return fmt.Errorf("error getting account from keybase: %w", err)
		}

		overwrite, _ := f.GetBool(flagOverwrite)
		if multisig != "" {
			// Bech32 decode error, maybe it's a name, we try to fetch from keyring
			multisigAddr, multisigName, _, err := client.GetFromFields(clientCtx, txF.Keybase(), multisig)
			if err != nil {
				return fmt.Errorf("error getting account from keybase: %w", err)
			}
			multisigkey, err := getMultisigRecord(clientCtx, multisigName)
			if err != nil {
				return err
			}
			multisigPubKey, err := multisigkey.GetPubKey()
			if err != nil {
				return err
			}
			multisigLegacyPub := multisigPubKey.(*kmultisig.LegacyAminoPubKey)

			fromRecord, err := clientCtx.Keyring.Key(fromName)
			if err != nil {
				return fmt.Errorf("error getting account from keybase: %w", err)
			}
			fromPubKey, err := fromRecord.GetPubKey()
			if err != nil {
				return err
			}

			var found bool
			for _, pubkey := range multisigLegacyPub.GetPubKeys() {
				if pubkey.Equals(fromPubKey) {
					found = true
				}
			}
			if !found {
				return fmt.Errorf("signing key is not a part of multisig key")
			}
			err = authclient.SignTxWithSignerAddress(
				txF, clientCtx, multisigAddr, fromName, txBuilder, clientCtx.Offline, overwrite)
			if err != nil {
				return err
			}
			printSignatureOnly = true
		} else {
			err = authclient.SignTx(txF, clientCtx, clientCtx.GetFromName(), txBuilder, clientCtx.Offline, overwrite)
		}
		if err != nil {
			return err
		}

		aminoJSON, err := f.GetBool(flagAmino)
		if err != nil {
			return err
		}

		bMode, err := f.GetString(flags.FlagBroadcastMode)
		if err != nil {
			return err
		}

		var json []byte
		if aminoJSON {
			stdTx, err := tx.ConvertTxToStdTx(clientCtx.LegacyAmino, txBuilder.GetTx())
			if err != nil {
				return err
			}
			req := BroadcastReq{
				Tx:   stdTx,
				Mode: bMode,
			}
			json, err = clientCtx.LegacyAmino.MarshalJSON(req)
			if err != nil {
				return err
			}
		} else {
			json, err = marshalSignatureJSON(txCfg, txBuilder, printSignatureOnly)
			if err != nil {
				return err
			}
		}

		outputDoc, _ := cmd.Flags().GetString(flags.FlagOutputDocument)
		if outputDoc == "" {
			cmd.Printf("%s\n", json)
			return nil
		}

		fp, err := os.OpenFile(outputDoc, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		defer func() {
			err2 := fp.Close()
			if err == nil {
				err = err2
			}
		}()

		_, err = fp.Write(append(json, '\n'))
		return err
	}
}

func marshalSignatureJSON(txConfig client.TxConfig, txBldr client.TxBuilder, signatureOnly bool) ([]byte, error) {
	parsedTx := txBldr.GetTx()
	if signatureOnly {
		sigs, err := parsedTx.GetSignaturesV2()
		if err != nil {
			return nil, err
		}
		return txConfig.MarshalSignatureJSON(sigs)
	}

	return txConfig.TxJSONEncoder()(parsedTx)
}
