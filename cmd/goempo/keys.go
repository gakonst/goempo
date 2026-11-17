package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/validator"
)

const keysUsage = "usage: goempo keys generate|public|add-validator|rotate-validator [flags]"

var errKeysUsage = errors.New(keysUsage)

func runKeys(args []string) int {
	if err := keys(os.Stdout, args); err != nil {
		fmt.Fprintln(os.Stderr, "goempo keys:", err)
		return 2
	}
	return 0
}

func keys(out io.Writer, args []string) error {
	if len(args) == 0 {
		return errKeysUsage
	}
	switch args[0] {
	case "generate":
		return keysGenerate(out, args[1:])
	case "public":
		return keysPublic(out, args[1:])
	case "add-validator", "rotate-validator":
		return keysRegistration(out, args[0], args[1:])
	default:
		return errKeysUsage
	}
}

func keysGenerate(out io.Writer, args []string) error {
	fs := flag.NewFlagSet("goempo keys generate", flag.ContinueOnError)
	output := fs.String("output", "", "file to write the 0x-hex ed25519 seed to (mode 0600)")
	force := fs.Bool("force", false, "overwrite an existing file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *output == "" {
		return errors.New("--output is required")
	}
	k, err := validator.NewSigningKey(rand.Reader)
	if err != nil {
		return err
	}
	if err := validator.WriteSigningKey(*output, k, validator.Overwrite(*force)); err != nil {
		return err
	}
	pub := k.Public()
	_, err = fmt.Fprintf(out, "0x%x\n", pub)
	return err
}

func keysPublic(out io.Writer, args []string) error {
	fs := flag.NewFlagSet("goempo keys public", flag.ContinueOnError)
	path := fs.String("signing-key", "", "ed25519 signing key file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	k, err := validator.ReadSigningKey(*path)
	if err != nil {
		return err
	}
	pub := k.Public()
	_, err = fmt.Fprintf(out, "0x%x\n", pub)
	return err
}

type registrationFlags struct {
	key     string
	chainID uint64
	who     string
	ingress string
	egress  string
	fee     string
	index   uint64
	rpc     string
}

func keysRegistration(out io.Writer, kind string, args []string) error {
	var f registrationFlags
	fs := flag.NewFlagSet("goempo keys "+kind, flag.ContinueOnError)
	fs.StringVar(&f.key, "signing-key", "", "ed25519 signing key file")
	fs.Uint64Var(&f.chainID, "chain-id", 0, "chain id the signature is bound to")
	fs.StringVar(&f.who, "validator", "", "validator address (the account that owns the entry)")
	fs.StringVar(&f.ingress, "ingress", "", "p2p ingress ip:port")
	fs.StringVar(&f.egress, "egress", "", "p2p egress ip")
	fs.StringVar(&f.fee, "fee-recipient", "", "fee recipient (add-validator)")
	fs.Uint64Var(&f.index, "index", 0, "validator index (rotate-validator)")
	fs.StringVar(&f.rpc, "simulate-rpc", "", "add-validator: eth_call the registration from the config owner on this JSON-RPC node (never sends a transaction)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	k, err := validator.ReadSigningKey(f.key)
	if err != nil {
		return err
	}
	who, err := primitives.HexToAddress(f.who)
	if err != nil {
		return err
	}
	r, err := validator.Registration{ChainID: f.chainID, Validator: who, PublicKey: k.Public(), Ingress: f.ingress, Egress: f.egress}.Canonical()
	if err != nil {
		return err
	}
	if kind == "rotate-validator" {
		msg := r.RotateMessage()
		sig := r.SignRotate(k)
		return printRegistration(out, registrationOutput{message: msg[:], signature: sig[:], calldata: r.RotateCalldata(f.index, sig[:])})
	}
	fee, err := primitives.HexToAddress(f.fee)
	if err != nil {
		return err
	}
	msg := r.AddMessage(fee)
	sig := r.SignAdd(k, fee)
	if err := printRegistration(out, registrationOutput{message: msg[:], signature: sig[:], calldata: r.AddCalldata(fee, sig[:])}); err != nil {
		return err
	}
	if f.rpc == "" {
		return nil
	}
	j, err := validator.CheckJoin(newRPCCaller(f.rpc), r, fee, sig[:])
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "simulated: chain %d owner %s validators %d: addValidator from the owner would succeed as index %d using %d gas\n",
		j.ChainID, j.Owner, j.Count, j.SimulatedIndex, j.Gas)
	return err
}

type registrationOutput struct {
	message   []byte
	signature []byte
	calldata  []byte
}

func printRegistration(out io.Writer, r registrationOutput) error {
	_, err := fmt.Fprintf(out, "to:        %s\nmessage:   0x%s\nsignature: 0x%s\ncalldata:  0x%s\nnot submitted: send it from the validator config owner yourself\n",
		validator.ValidatorConfigV2, hex.EncodeToString(r.message), hex.EncodeToString(r.signature), hex.EncodeToString(r.calldata))
	return err
}
