"""Resolve the v2.24 client's NOTI/CMD handler function for a given id from a crash dump."""
import sys
import struct

sys.path.insert(0, r"D:\DNF project\service\美服100服务端\tools")
from diagnose_dump import Dump

FNV_OFFSET = 0xCBF29CE484222325
FNV_PRIME = 0x100000001B3


def fnv1a64_u32(value):
    h = FNV_OFFSET
    for byte in struct.pack("<I", value & 0xFFFFFFFF):
        h = ((h ^ byte) * FNV_PRIME) & 0xFFFFFFFFFFFFFFFF
    return h


def qword(dump, address):
    data = dump.memory(address, 8)
    if len(data) != 8:
        return None
    return struct.unpack("<Q", data)[0]


def resolve(dump, manager, target_id, limit=16):
    mask = qword(dump, manager + 0x40)
    buckets = qword(dump, manager + 0x28)
    end = qword(dump, manager + 0x10)
    if mask is None or buckets is None or end is None:
        return None, "manager map unreadable"
    index = fnv1a64_u32(target_id) & mask
    node = qword(dump, buckets + index * 16 + 8)
    seen = set()
    while node is not None and node != end and node not in seen and len(seen) < limit:
        seen.add(node)
        node_id = qword(dump, node + 0x10)
        if node_id == target_id:
            handler = qword(dump, node + 0x18)
            if handler is None:
                return None, "handler node unreadable"
            fn = qword(dump, handler)
            return {"handler_object": hex(handler), "function": hex(fn) if fn else None}, None
        node = qword(dump, node + 8)
    return None, "id not present in map (buckets=%#x mask=%#x end=%#x)" % (buckets, mask, end)


def main():
    dump_path = sys.argv[1]
    dump = Dump(dump_path)
    try:
        manager_global = qword(dump, 0x140000000 + 0x9788B38)
        print("manager global [0x9788b38] =", hex(manager_global) if manager_global else None)
        if not manager_global:
            return
        for raw in sys.argv[2:]:
            target = int(raw, 0)
            info, error = resolve(dump, manager_global, target)
            print("id %-6d -> %s %s" % (target, info, error or ""))
    finally:
        dump.f.close()


if __name__ == "__main__":
    main()
