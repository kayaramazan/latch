package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/kayaramazan/latch/latch-relay/internal/acme"
	"github.com/kayaramazan/latch/latch-relay/internal/store"
)

func cmdDomain(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: latch-relay domain <add|list|rm|verify> ...")
	}
	switch args[0] {
	case "add":
		return cmdDomainAdd(args[1:])
	case "list", "ls":
		return cmdDomainList(args[1:])
	case "rm", "remove", "delete":
		return cmdDomainRm(args[1:])
	case "verify":
		return cmdDomainVerify(args[1:])
	default:
		return fmt.Errorf("domain: unknown subcommand %q", args[0])
	}
}

func cmdDomainAdd(args []string) error {
	fs := flag.NewFlagSet("domain add", flag.ContinueOnError)
	dataDir := fs.String("data-dir", defaultDataDir(), "data directory")
	note := fs.String("note", "", "free-text note")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: latch-relay domain add <hostname> [--note ...]")
	}
	st, err := openStore(*dataDir)
	if err != nil {
		return err
	}
	defer st.Close()
	ctx := context.Background()
	target, _ := st.Setting(ctx, store.SettingPublicHost)
	if err := st.AddCustomDomain(ctx, fs.Arg(0), target, *note); err != nil {
		return err
	}
	fmt.Printf("Registered custom domain %s.\n", fs.Arg(0))
	if target != "" {
		fmt.Printf("Point it at this relay with a CNAME to %s, then run:\n  latch-relay domain verify %s\n", target, fs.Arg(0))
	}
	return nil
}

func cmdDomainList(args []string) error {
	fs := flag.NewFlagSet("domain list", flag.ContinueOnError)
	dataDir := fs.String("data-dir", defaultDataDir(), "data directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := openStore(*dataDir)
	if err != nil {
		return err
	}
	defer st.Close()
	domains, err := st.ListCustomDomains(context.Background())
	if err != nil {
		return err
	}
	if len(domains) == 0 {
		fmt.Println("no custom domains registered")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "DOMAIN\tCNAME TARGET\tVERIFIED\tNOTE")
	for _, d := range domains {
		verified := "no"
		if d.VerifiedAt != nil {
			verified = d.VerifiedAt.Format(time.RFC3339)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", d.Domain, d.Target, verified, d.Note)
	}
	return tw.Flush()
}

func cmdDomainRm(args []string) error {
	fs := flag.NewFlagSet("domain rm", flag.ContinueOnError)
	dataDir := fs.String("data-dir", defaultDataDir(), "data directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: latch-relay domain rm <hostname>")
	}
	st, err := openStore(*dataDir)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.RemoveCustomDomain(context.Background(), fs.Arg(0)); err != nil {
		return err
	}
	fmt.Printf("Removed custom domain %s.\n", fs.Arg(0))
	return nil
}

func cmdDomainVerify(args []string) error {
	fs := flag.NewFlagSet("domain verify", flag.ContinueOnError)
	dataDir := fs.String("data-dir", defaultDataDir(), "data directory")
	publicIP := fs.String("public-ip", "", "the relay's public IP (for A-record verification)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: latch-relay domain verify <hostname>")
	}
	st, err := openStore(*dataDir)
	if err != nil {
		return err
	}
	defer st.Close()
	ctx := context.Background()
	d, err := st.GetCustomDomain(ctx, fs.Arg(0))
	if err != nil {
		return fmt.Errorf("%s is not registered (add it first)", fs.Arg(0))
	}
	publicHost, _ := st.Setting(ctx, store.SettingPublicHost)
	vctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := acme.CheckPointsHere(vctx, acme.DefaultResolver(), d.Domain, publicHost, *publicIP); err != nil {
		return fmt.Errorf("%s does not point here yet: %w", d.Domain, err)
	}
	if err := st.SetCustomDomainVerified(ctx, d.Domain, time.Now()); err != nil {
		return err
	}
	fmt.Printf("%s points at this relay. A certificate will be issued on first HTTPS request.\n", d.Domain)
	return nil
}
