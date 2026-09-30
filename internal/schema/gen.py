#!/usr/bin/env python3
"""Generate the request-body JSON Schemas that `spicrawl schema` embeds.

Reads ../../openapi.yaml (relative to this file, at the repo root), resolves every
$ref inline and writes one <name>.json per request body next to this script.
Run it through `go generate ./internal/schema/` after editing the OpenAPI spec.

Recursive definitions cannot be inlined, so a schema that refers back to
itself (extract rules nest selector maps) is placed once under `$defs` and
referenced as `#/$defs/<Name>`. Every output is self-contained: no $ref
points outside the file.

Requires python3 and PyYAML.
"""

import json
import os
import sys

import yaml

HERE = os.path.dirname(os.path.abspath(__file__))
SPEC = os.path.join(HERE, "..", "..", "openapi.yaml")

# CLI name -> (components/schemas name, description of where it is sent)
BODIES = {
    "scrape": ("ScrapeRequest", "Body of POST /v1/scrape"),
    "batch": ("BatchCreateRequest", "Body of POST /v1/batch and POST /v1/batch/{batchID}/items"),
    "batch-item": ("BatchItem", "One element of items[] in a batch body"),
    "session": ("SessionCreateRequest", "Body of POST /v1/sessions"),
}


def pointer(doc, ref):
    if not ref.startswith("#/"):
        sys.exit(f"gen.py: external $ref not supported: {ref}")
    node = doc
    for part in ref[2:].split("/"):
        part = part.replace("~1", "/").replace("~0", "~")
        node = node[part]
    return node


def ref_name(ref):
    return ref.rsplit("/", 1)[-1]


class Resolver:
    def __init__(self, doc):
        self.doc = doc
        self.defs = {}

    def resolve(self, node, stack=()):
        if isinstance(node, list):
            return [self.resolve(v, stack) for v in node]
        if not isinstance(node, dict):
            return node
        if "$ref" in node:
            ref = node["$ref"]
            siblings = {k: v for k, v in node.items() if k != "$ref"}
            if ref in stack:
                # Cycle: hoist the target into $defs once and point at it.
                name = ref_name(ref)
                if name not in self.defs:
                    self.defs[name] = None  # placeholder breaks re-entry
                    self.defs[name] = self.resolve(pointer(self.doc, ref), (ref,))
                target = {"$ref": "#/$defs/" + name}
            else:
                target = self.resolve(pointer(self.doc, ref), stack + (ref,))
            if not siblings:
                return target
            # 2020-12 allows siblings next to $ref; merge them over the target.
            out = dict(target) if "$ref" not in target else {"allOf": [target]}
            for k, v in siblings.items():
                out[k] = self.resolve(v, stack)
            return out
        return {k: self.resolve(v, stack) for k, v in node.items()}


def main():
    with open(SPEC) as fh:
        doc = yaml.safe_load(fh)
    schemas = doc["components"]["schemas"]
    for name, (component, where) in BODIES.items():
        r = Resolver(doc)
        ref = "#/components/schemas/" + component
        body = r.resolve(schemas[component], (ref,))
        out = {
            "$schema": "https://json-schema.org/draft/2020-12/schema",
            "title": component,
            "x-spicrawl-endpoint": where,
        }
        out.update(body)
        if r.defs:
            out["$defs"] = r.defs
        text = json.dumps(out, indent=2, ensure_ascii=False, sort_keys=False) + "\n"
        if '"#/components/' in text:
            sys.exit(f"gen.py: unresolved $ref left in {name}")
        with open(os.path.join(HERE, name + ".json"), "w") as fh:
            fh.write(text)
        print(f"wrote {name}.json ({len(text)} bytes)")


if __name__ == "__main__":
    main()
