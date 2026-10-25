#!/usr/bin/env python3
"""make-zlib-move.py <flat main> <out.ips>: move Duck Game's zlib packet output buffer from main+0x8213000
to main+0x821F800 (the end of the 0x10C800 NEX pool) and cap it at 0x10000/0x10004, so the pool and the
compressor no longer share memory. Every patched site is checked against the expected original word."""
import struct, sys, capstone
flat = bytearray(open(sys.argv[1], 'rb').read())
OLD, NEW = 0x8213000, 0x821F800
def word(a): return struct.unpack_from('<I', flat, a)[0]
def adrp(pc, rd, target):
    imm = ((target & ~0xfff) - (pc & ~0xfff)) >> 12
    return 0x90000000 | ((imm & 3) << 29) | (((imm >> 2) & 0x7ffff) << 5) | rd
def add_imm(rd, rn, imm): return 0x91000000 | (imm << 10) | (rn << 5) | rd
def movk_w(rd, imm, hw): return 0x72800000 | (hw << 21) | (imm << 5) | rd
def movz_w(rd, imm, hw): return 0x52800000 | (hw << 21) | (imm << 5) | rd
edits = []
# base moves: (adrp pc, register) followed by add rd,rd,#0
for pc, rd in [(0xeda700, 0), (0xeda758, 1), (0xeda894, 0), (0xeda8ec, 1), (0xedac40, 21), (0xedacc8, 2), (0xedad14, 2)]:
    assert word(pc) == adrp(pc, rd, OLD), hex(pc)
    assert word(pc + 4) == add_imm(rd, rd, 0), hex(pc + 4)
    edits += [(pc, adrp(pc, rd, NEW)), (pc + 4, add_imm(rd, rd, NEW & 0xfff))]
# receive: uncompressed-size check and DecompressZlib capacity 0x20004 -> 0x10004 (high half 2 -> 1)
for pc, rd in [(0xeda6e0, 8), (0xeda70c, 1), (0xeda874, 8), (0xeda8a0, 1)]:
    assert word(pc) == movk_w(rd, 2, 1), hex(pc)
    edits.append((pc, movk_w(rd, 1, 1)))
# send: bound 0x20003 -> 0x10003, CompressZlib capacity 0x20000 -> 0x10000
assert word(0xedac30) == movk_w(9, 2, 1); edits.append((0xedac30, movk_w(9, 1, 1)))
assert word(0xedac64) == 0x320f03e2; edits.append((0xedac64, 0x321003e2))  # orr w2,wzr,#0x20000 -> #0x10000
md = capstone.Cs(capstone.CS_ARCH_ARM64, capstone.CS_MODE_ARM)
recs = b''
for pc, w in edits:
    old = next(md.disasm(struct.pack('<I', word(pc)), pc)); new = next(md.disasm(struct.pack('<I', w), pc))
    print(f"{pc:#x}: {old.mnemonic} {old.op_str:24} -> {new.mnemonic} {new.op_str}")
    off = pc + 0x100
    recs += off.to_bytes(3, 'big') + struct.pack('>H', 4) + struct.pack('<I', w)
open(sys.argv[2], 'wb').write(b'PATCH' + recs + b'EOF')
print(len(edits), "instructions,", sys.argv[2])
