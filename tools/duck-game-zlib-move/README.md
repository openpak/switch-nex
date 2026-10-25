# Duck Game packet-buffer patch

> **Not approved for release yet.** This replaces the withdrawn NEX heap-size patch
> (`openpak_duck_game_nex_heap`, 0xED9B18), which broke online play. It needs the owner's
> approval as a game-patch exception and a console test in both directions before it ships.

## Why a patch

Duck Game hands NEX a memory pool at main+0x8113000 and declares its size as 0x10C800. The array
behind it is only 0x100000, so the pool's last 0xC800 bytes (main+0x8213000 to main+0x821F800)
are the start of the game's own zlib packet output buffer. NEX places a thread stack there at
sign-in (the secure WebSocket connect thread), and the kernel locks a mapped stack's original
memory. The first packet the game compresses or decompresses then writes into that locked memory,
and a real Switch aborts ("The software was closed because an error occurred"). Emulators do not
enforce the lock. Shrinking the pool to 0x100000 is not an option: NEX needs the full 0x10C800 and
signs out within a second without it.

## What the patch changes

The pool stays as the game declares it. The packet output buffer moves to the pool's end, and its
capacity drops from 0x20000/0x20004 to 0x10000/0x10004, so it fits in the same static array before
the zlib work buffer:

| Region | Before | After |
|---|---|---|
| NEX pool | 0x8113000-0x821F800 | unchanged |
| packet output (header + data) | 0x8213000-0x8233004 | 0x821F800-0x822F804 |
| zlib work buffer | 0x8233004-0x8274744 | unchanged |

20 instructions in the compressor and decompressor, all for build
`D5446193E5BC2E4C17F552597E14DDB7` (update v262144):
- 7 `adrp`/`add` pairs that load the output buffer base: 0x8213000 becomes 0x821F800.
- The receive size check and the DecompressZlib capacity, at both receive sites:
  `movk #2, lsl 16` becomes `movk #1, lsl 16` (0x20004 to 0x10004).
- The send bound check (0x20003 to 0x10003) and the CompressZlib capacity
  (`orr w2, wzr, #0x20000` to `#0x10000`).

The game builds packets of up to about 9000 bytes plus resends, so 64 KB is ample. A larger
packet is skipped exactly as before, when it would not fit in 128 KB.

No other code or relocation refers to either output range. The script checks every original word
before it writes anything.

`make-ips.py <decompressed main> <out.ips>` regenerates the IPS (offsets include the 0x100 NSO header).
