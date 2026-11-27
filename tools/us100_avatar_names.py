# -*- coding: utf-8 -*-
"""Compose Chinese names only from fully recognized outfit collection/part names."""
import re


VARIANT = re.compile(r"^(.*?)\s*\[Type ([A-Z])\]$")
START = re.compile(r"^([0-9]{4})(?:\s+|\s*-\s*)")
TAG = re.compile(r"(\[[^\[\]]+\])$")


class AvatarNames:
    def __init__(self, path):
        dictionaries = {"collection": {}, "part": {}}
        current = None
        for line in path.read_text(encoding="utf-8-sig").splitlines():
            line = line.strip()
            if not line or line.startswith("#"):
                continue
            if line in ("[collection]", "[part]"):
                current = dictionaries[line[1:-1]]
                continue
            key, sep, value = line.partition("=")
            if current is None or not sep or not key or not value:
                raise ValueError(f"invalid avatar dictionary row: {line}")
            key = key.casefold()
            if key in current and current[key] != value:
                raise ValueError(f"conflicting avatar term: {key}")
            current[key] = value
        self.collections = sorted(dictionaries["collection"].items(), key=lambda pair: -len(pair[0]))
        self.parts = dictionaries["part"]
        self.max_parts = max(len(k.split()) for k in self.parts)

    def translate(self, text):
        match = VARIANT.fullmatch(text)
        if not match:
            return None
        name, variant = match.groups()
        normalized = name.casefold().replace("’", "'")
        for prefix, title in self.collections:
            if not normalized.startswith(prefix + " "):
                continue
            remaining = normalized[len(prefix):].strip(" -")
            if prefix == "hot summer" and (year := START.match(remaining)):
                title = year[1] + title
                remaining = remaining[year.end():].strip(" -")
            parts = self._components(remaining)
            if parts:
                return title + "·" + parts + f" [{variant}型]"
        return None

    def _components(self, text):
        age_suffix = ""
        if category := TAG.search(text):
            role = category[1][1:-1]
            if role not in self.parts:
                return None
            age_suffix = "[" + self.parts[role] + "]"
            text = text[:category.start()].strip()
        cleaned = re.sub(r"(?<=\w)'s\b", "", text)
        tokens = cleaned.split()
        out = []
        while tokens:
            for length in range(min(len(tokens), self.max_parts), 0, -1):
                match = " ".join(tokens[:length])
                if match in self.parts:
                    out.append(self.parts[match])
                    tokens = tokens[length:]
                    break
            else:
                return None
        return "".join(out) + age_suffix if out else None
