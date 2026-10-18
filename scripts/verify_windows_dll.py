"""Check a Windows TurboVec DLL: it must export every od_tv_* function the runtime binds and must not need the
Visual C++ runtime, which a clean Windows install may lack.

Usage: python3 scripts/verify_windows_dll.py path/to/overdrive_turbovec_ffi.dll [...]
"""

from __future__ import annotations

import struct
import sys
from pathlib import Path

REQUIRED_EXPORTS = {"od_tv_open", "od_tv_close", "od_tv_add", "od_tv_remove", "od_tv_search", "od_tv_sync"}
FORBIDDEN_IMPORT_PREFIXES = ("vcruntime", "msvcp")


class PE:
    def __init__(self, data: bytes) -> None:
        self.data = data
        pe = struct.unpack_from("<I", data, 0x3C)[0]
        if data[pe:pe + 4] != b"PE\0\0":
            raise ValueError("not a PE file")
        sections = struct.unpack_from("<H", data, pe + 6)[0]
        optional_size = struct.unpack_from("<H", data, pe + 20)[0]
        optional = pe + 24
        magic = struct.unpack_from("<H", data, optional)[0]
        self.directories = optional + (112 if magic == 0x20B else 96)
        table = optional + optional_size
        self.sections = [
            (
                struct.unpack_from("<I", data, table + i * 40 + 12)[0],
                struct.unpack_from("<I", data, table + i * 40 + 8)[0],
                struct.unpack_from("<I", data, table + i * 40 + 20)[0],
            )
            for i in range(sections)
        ]

    def offset(self, rva: int) -> int:
        for start, size, raw in self.sections:
            if start <= rva < start + max(size, 1):
                return raw + rva - start
        raise ValueError(f"rva {rva:#x} outside every section")

    def cstring(self, rva: int) -> str:
        at = self.offset(rva)
        return self.data[at:self.data.index(b"\0", at)].decode()

    def directory(self, index: int) -> int:
        return struct.unpack_from("<I", self.data, self.directories + index * 8)[0]

    def exports(self) -> set[str]:
        rva = self.directory(0)
        if not rva:
            return set()
        at = self.offset(rva)
        count = struct.unpack_from("<I", self.data, at + 24)[0]
        names = self.offset(struct.unpack_from("<I", self.data, at + 32)[0])
        return {self.cstring(struct.unpack_from("<I", self.data, names + 4 * i)[0]) for i in range(count)}

    def imports(self) -> list[str]:
        rva = self.directory(1)
        out = []
        at = self.offset(rva) if rva else 0
        while rva:
            name = struct.unpack_from("<I", self.data, at + 12)[0]
            if not name:
                break
            out.append(self.cstring(name))
            at += 20
        return out


def problems(path: Path) -> list[str]:
    pe = PE(path.read_bytes())
    found = []
    missing = sorted(REQUIRED_EXPORTS - pe.exports())
    if missing:
        found.append(f"missing exports {missing}")
    needs = [name for name in pe.imports() if name.lower().startswith(FORBIDDEN_IMPORT_PREFIXES)]
    if needs:
        found.append(f"needs the Visual C++ runtime {needs}")
    return found


def main(paths: list[str]) -> int:
    failed = False
    for raw in paths:
        issues = problems(Path(raw))
        print(f"{raw}: {'ok' if not issues else '; '.join(issues)}")
        failed = failed or bool(issues)
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
