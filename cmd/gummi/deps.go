package main

import (
	"context"
	"fmt"
)

// runDepsAdd implements `gummi deps add <dependent> <depends-on>`: record
// that the first card depends on the second. It surfaces the store's typed
// errors (self-loop, cycle, late attachment, unknown card) verbatim and
// exits non-zero; a successful add prints the edge and exits zero.
func runDepsAdd(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("deps add needs exactly two cards: <dependent> <depends-on>")
	}
	de, err := openCardEnv()
	if err != nil {
		return err
	}
	defer de.cleanup()
	ctx := context.Background()
	dep, err := resolveCardID(ctx, de.store, args[0])
	if err != nil {
		return err
	}
	target, err := resolveCardID(ctx, de.store, args[1])
	if err != nil {
		return err
	}
	if err := de.store.AddDependency(ctx, dep, target); err != nil {
		return err
	}
	fmt.Printf("  %s depends on %s\n", dep, target)
	return nil
}

// runDepsRm implements `gummi deps rm <dependent> <depends-on>`: remove
// the edge. Removing an edge that does not exist is an idempotent no-op that
// exits zero (the store owns that semantics).
func runDepsRm(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("deps rm needs exactly two cards: <dependent> <depends-on>")
	}
	de, err := openCardEnv()
	if err != nil {
		return err
	}
	defer de.cleanup()
	ctx := context.Background()
	dep, err := resolveCardID(ctx, de.store, args[0])
	if err != nil {
		return err
	}
	target, err := resolveCardID(ctx, de.store, args[1])
	if err != nil {
		return err
	}
	if err := de.store.RemoveDependency(ctx, dep, target); err != nil {
		return err
	}
	fmt.Printf("  %s no longer depends on %s\n", dep, target)
	return nil
}

// runDepsList implements `gummi deps list <id>`: print each forward edge
// (what the card depends on) as one FD-NNN per line, in the store's number
// order.
func runDepsList(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("deps list needs exactly one card: <id>")
	}
	de, err := openCardEnv()
	if err != nil {
		return err
	}
	defer de.cleanup()
	ctx := context.Background()
	id, err := resolveCardID(ctx, de.store, args[0])
	if err != nil {
		return err
	}
	deps, err := de.store.ListDependencies(ctx, id)
	if err != nil {
		return err
	}
	for _, d := range deps {
		fmt.Println(d)
	}
	return nil
}
