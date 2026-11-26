"""Build desktop icons from the existing whale, preserving its pixels and identity.
Requires Pillow; macOS iconutil creates the .icns container.
"""
from pathlib import Path
from PIL import Image, ImageDraw
import io
import struct
import subprocess
import tempfile

root = Path(__file__).resolve().parents[1]
scale = 2
canvas = Image.new('RGBA', (1024 * scale, 1024 * scale))
draw = ImageDraw.Draw(canvas)
draw.rounded_rectangle((64 * scale, 64 * scale, 960 * scale, 960 * scale), radius=196 * scale, fill='white')
whale = Image.open(root / 'assets/whale-source.png').convert('RGBA').resize((768 * scale, 768 * scale), Image.Resampling.LANCZOS)
canvas.alpha_composite(whale, (128 * scale, 128 * scale))
canvas = canvas.resize((1024, 1024), Image.Resampling.LANCZOS)
canvas.save(root / 'assets/icon.png')
with tempfile.TemporaryDirectory() as stage:
    iconset = Path(stage) / 'icon.iconset'
    iconset.mkdir()
    for size in (16, 32, 128, 256, 512):
        for suffix, multiplier in (('', 1), ('@2x', 2)):
            canvas.resize((size * multiplier,) * 2, Image.Resampling.LANCZOS).save(iconset / f'icon_{size}x{size}{suffix}.png')
    subprocess.run(['iconutil', '-c', 'icns', str(iconset), '-o', str(root / 'assets/icon.icns')], check=True)
buffer = io.BytesIO()
canvas.resize((256, 256), Image.Resampling.LANCZOS).save(buffer, format='PNG')
data = buffer.getvalue()
(root / 'assets/icon.ico').write_bytes(struct.pack('<HHH', 0, 1, 1) + struct.pack('<BBBBHHII', 0, 0, 0, 0, 1, 32, len(data), 22) + data)
