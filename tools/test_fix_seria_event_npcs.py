# -*- coding: utf-8 -*-
import struct
import sys
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
sys.path[:0] = [str(ROOT / 'tools')]

import fix_seria_event_npcs as mod  # noqa: E402


class TestFixSeriaEventNPCs(unittest.TestCase):
    def test_patch_gate_seria_map_inserts_npcs_idempotently(self):
        tokens = [
            {'type': 3, 'value': 38418376, 'text': '[NPC]'},
            {'type': 0, 'value': 2},
            {'type': 6, 'value': 146359016, 'text': '[right]'},
            {'type': 0, 'value': 555},
            {'type': 0, 'value': 146},
            {'type': 0, 'value': 0},
            {'type': 0, 'value': 20673},
            {'type': 6, 'value': 146355922, 'text': '[left]'},
            {'type': 0, 'value': 340},
            {'type': 0, 'value': 0},
            {'type': 0, 'value': 0},
            {'type': 3, 'value': 43304144, 'text': '[/NPC]'},
        ]
        raw = b''.join(struct.pack('<Bi', t['type'], t['value']) for t in tokens)
        patched, added = mod.patch_gate_seria_map_bytes(raw, tokens)
        self.assertEqual(added, [100000827, 500000169])
        self.assertEqual(len(patched), len(raw) + 2 * 25)
        # Verify [/NPC] remains at the tail
        self.assertEqual(patched[-5:], struct.pack('<Bi', 3, 43304144))

        # Decode back into tokens and verify idempotency
        toks2 = []
        for off in range(0, len(patched), 5):
            tt, val = struct.unpack_from('<Bi', patched, off)
            t = {'type': tt, 'value': val}
            if val == 38418376:
                t['text'] = '[NPC]'
            elif val == 43304144:
                t['text'] = '[/NPC]'
            elif val == 146359016:
                t['text'] = '[right]'
            elif val == 146355922:
                t['text'] = '[left]'
            toks2.append(t)
        patched2, added2 = mod.patch_gate_seria_map_bytes(patched, toks2)
        self.assertEqual(added2, [])
        self.assertEqual(patched2, patched)

    def test_patch_himan_npc_visible_event(self):
        tokens = [
            {'type': 3, 'value': 174592612, 'text': '[default npc visible]'},
            {'type': 0, 'value': 0},
            {'type': 3, 'value': 174592570, 'text': '[open visible event]'},
            {'type': 0, 'value': 426},
        ]
        raw = b''.join(struct.pack('<Bi', t['type'], t['value']) for t in tokens)
        patched, changed = mod.patch_himan_npc_visible_event(raw, tokens)
        self.assertTrue(changed)
        tt, val = struct.unpack_from('<Bi', patched, 15)
        self.assertEqual((tt, val), (0, 614))

        tokens[3]['value'] = 614
        patched2, changed2 = mod.patch_himan_npc_visible_event(patched, tokens)
        self.assertFalse(changed2)
        self.assertEqual(patched2, patched)


if __name__ == '__main__':
    unittest.main()
