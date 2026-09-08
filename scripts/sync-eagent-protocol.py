#!/usr/bin/env python3
"""Copy the versioned stdlib Go wire definitions and SDK into eagent.

Run after changing the protocol. --check verifies a checkout without editing it.
The integration remains a dependency-free Go module with its own release cycle.
"""
import argparse
import pathlib
import sys

parser = argparse.ArgumentParser()
parser.add_argument("eagent", type=pathlib.Path)
parser.add_argument("--check", action="store_true")
args = parser.parse_args()
root = pathlib.Path(__file__).resolve().parents[1]
pairs = [("internal/artifact/manifest.go", "internal/protocol/artifact/manifest.go"),
         ("internal/control/protocol.go", "internal/protocol/control/protocol.go"),
         ("sdk/finale-artifact.js", "internal/archive/assets/finale-artifact.js")]
bad = False
for source, destination in pairs:
    text = (root / source).read_text()
    if source.endswith(".go"):
        text = "// Code generated from FinaleChat " + source + "; DO NOT EDIT.\n" + text
        text = text.replace("github.com/ericflo/finalechat/internal/artifact", "github.com/ericflo/eagent/internal/protocol/artifact")
    target = args.eagent / destination
    if args.check:
        if not target.exists() or target.read_text() != text:
            print("Out of sync:", target, file=sys.stderr)
            bad = True
    else:
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(text)
sys.exit(1 if bad else 0)
