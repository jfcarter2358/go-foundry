#!/usr/bin/env python3
"""Writes the binary and re-encoded MusicXML fixtures from the text ones.

compressed.mxl   simple.musicxml zipped as an .mxl: a META-INF/container.xml
                 pointing at the score in a subfolder (and a decoy .xml
                 beside it the container does not name), as Sibelius and
                 MuseScore write them.
utf16.musicxml   sibelius_piano.musicxml in UTF-16 with a byte-order mark,
                 the way Finale saves.
Run it from this directory.
"""
import zipfile

simple = open('simple.musicxml', encoding='utf-8').read()
container = '''<?xml version="1.0" encoding="UTF-8"?>
<container>
  <rootfiles>
    <rootfile full-path="score/simple_tune.musicxml" media-type="application/vnd.recordare.musicxml+xml"/>
  </rootfiles>
</container>
'''
with zipfile.ZipFile('compressed.mxl', 'w', zipfile.ZIP_DEFLATED) as z:
    z.writestr('mimetype', 'application/vnd.recordare.musicxml', compress_type=zipfile.ZIP_STORED)
    z.writestr('META-INF/container.xml', container)
    z.writestr('aaa_decoy.xml', '<not-a-score/>')
    z.writestr('score/simple_tune.musicxml', simple)

piano = open('sibelius_piano.musicxml', encoding='utf-8').read()
piano = piano.replace('encoding="UTF-8"', 'encoding="UTF-16"', 1)
open('utf16.musicxml', 'wb').write(piano.encode('utf-16'))
