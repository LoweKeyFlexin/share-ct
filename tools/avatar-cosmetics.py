#!/usr/bin/env python3
"""Bake numeric native recipes and SVG-only gear masks; never copies source or raster assets.
Run after cmd/avatarbake with --fgc pointing at the native source checkout.
Requires Pillow and rsvg-convert at authoring time only. The deployed server needs neither.
"""
import argparse, hashlib, io, json, math, re, subprocess
from pathlib import Path
from PIL import Image, ImageChops


def base36(n):
    result = ''
    while n:
        result = '0123456789abcdefghijklmnopqrstuvwxyz'[n % 36] + result
        n //= 36
    return result or '0'


def native_recipes(root):
    theme = (root / 'CTCore/Theme.swift').read_text()
    enum = theme[theme.index('enum ThemePreset'):]
    raw = {}
    for match in re.finditer(r'^    case (\w+(?:,\s*\w+)*)(?: = "([a-z0-9]+)")?\s*$', enum, re.M):
        for name in match[1].split(','): raw[name.strip()] = match[2] or name.strip()

    palette_text = theme[theme.index('private var computePalette:'):theme.index('private static let paletteTable')]
    cases = list(re.finditer(r'case \.(\w+):\s*return ThemePalette\(', palette_text))
    palettes = {}
    for i, match in enumerate(cases):
        chunk = palette_text[match.end():cases[i+1].start() if i+1 < len(cases) else len(palette_text)]
        colors = {m[1]: [int(m[j]) for j in range(2, 5)] for m in re.finditer(r'\b(bg|ink|accent|accent2|accent3): c\((\d+),\s*(\d+),\s*(\d+)\)', chunk)}
        assert len(colors) == 5, (match[1], colors)
        palettes[match[1]] = colors
    assert len(palettes) >= 40
    skins = {}
    tokens = {}
    for name, palette in palettes.items():
        identity = raw[name]
        token = 2166136261
        for byte in identity.encode(): token = ((token ^ byte) * 16777619) & 0xffffffff
        token = 't' + format(token, '08x')
        tokens[name] = token
        a, b = palette['accent'], palette['accent2']
        dim = lambda value, factor: [round(x * factor, 6) for x in value]
        recipes = [(a,None,None), (a,b,None), (a,b,dim(palette['accent3'],.4)),
                   (palette['bg'],a,a), (palette['ink'],a,[0,0,0]), (a,[255,201,60],dim(a,.18)), (b,a,None)]
        for i,(line,edge,fill) in enumerate(recipes): skins[token+str(i)] = {'line':line,'edge':edge,'fill':fill}
    # Stable flat-color recipe introduced by the shared Cold Front drop.
    # Numeric metadata only; the public server never carries native app source.
    skins["f000000010"] = {"line": [46,83,183], "edge": None, "fill": None}
    colors_text = (root/'CTCore/DrillsColourCatalog.swift').read_text()
    colors = {m[1]:[float(m[j]) for j in range(2,5)] for m in re.finditer(r'\.init\(id:\s*"([a-z0-9-]+)",[^)]*?rgb:\s*CTRGB\(r:\s*([0-9.]+),\s*g:\s*([0-9.]+),\s*b:\s*([0-9.]+)\)', colors_text)}
    reserves_text = (root/'CTCore/ReservedCosmetics.swift').read_text()
    wire_block = reserves_text.split('static let wireColours = [',1)[1].split(']',1)[0]
    wires = re.findall(r'"([a-z0-9-]+)"',wire_block)
    body_block = reserves_text.split('static let fighterColours:',1)[1].split('static let colourIDs',1)[0]
    reserves = {}
    for body, entries in re.findall(r'"([a-z0-9-]+)":\s*\[([^]]*)\]',body_block):
        reserves[body] = {'c'+base36(wires.index(id)): colors[id] for id in re.findall(r'"([a-z0-9-]+)"', entries)}
    codes = dict(re.findall(r'case "([a-z0-9-]+)": return "([a-z0-9]+)"',reserves_text))
    gear = {'c': {'line':None,'edge':None,'fill':None}}
    for chunk in reserves_text.split('static let all: [Self] = [',1)[1].split('] + CTLimited',1)[0].split('.init(')[1:]:
        id = re.search(r'id: "([a-z0-9-]+)"',chunk)[1]
        if id == 'common': continue
        channels = {m[1]:[int(m[j]) for j in range(2,5)] for m in re.finditer(r'(tint|edge|fill): CTRGB\((\d+), (\d+), (\d+)\)',chunk)}
        gear[codes[id]] = {'line':channels['tint'],'edge':channels.get('edge'),'fill':channels.get('fill'),'glow':'glow: true' in chunk}
    order_text = (root/'CTCore/LimitedCosmeticsSchedule.swift').read_text().split('static let authoredOrder:',1)[1].split('= [',1)[1].split(']',1)[0]
    order = re.findall(r'\.(\w+)',order_text)
    for i,name in enumerate(order): gear['t'+base36(i)] = {'line':palettes[name]['accent'],'edge':palettes[name]['accent2'],'fill':None}
    allowed = {id:[code for code in gear if not code.startswith(('p','w')) and code!='m'] for id in ['knight-helmet','diving-helmet','scouter-visor','crash-helmet','switch-cap','mind-crown','joker-hat','katana','greatsword','bokken']}
    for id in ['katana','greatsword']: allowed[id] += ['w0','w1','w2']
    allowed['pirate-hat'] = ['p0','p1','p2']; allowed['madhouse-hat'] = ['m']
    return {'skins':skins,'bodySkins':{'puppet-champ':{tokens['madHouse']+'1':{'line':[255,46,62],'edge':[61,134,255],'fill':[6,7,15]}}},
            'reserves':reserves,'gear':gear,'allowedGear':allowed}


def contours(mask, side):
    """Trace pixel boundaries into small closed vector contours, including holes."""
    w,h = mask.size; pixels = mask.load(); edges = {}
    def edge(a,b): edges.setdefault(a,[]).append(b)
    for y in range(h):
        for x in range(w):
            if not pixels[x,y]: continue
            if y==0 or not pixels[x,y-1]: edge((x,y),(x+1,y))
            if x==w-1 or not pixels[x+1,y]: edge((x+1,y),(x+1,y+1))
            if y==h-1 or not pixels[x,y+1]: edge((x+1,y+1),(x,y+1))
            if x==0 or not pixels[x-1,y]: edge((x,y+1),(x,y))
    def simplify(points, tolerance=.65):
        if len(points)<3: return points
        a,b=points[0],points[-1]; dx,dy=b[0]-a[0],b[1]-a[1]
        denom=math.hypot(dx,dy)
        dist=lambda p: abs(dy*p[0]-dx*p[1]+b[0]*a[1]-b[1]*a[0])/denom if denom else math.dist(p,a)
        index=max(range(1,len(points)-1),key=lambda i:dist(points[i])); maximum=dist(points[index])
        if maximum<=tolerance:return [a,b]
        return simplify(points[:index+1],tolerance)[:-1]+simplify(points[index:],tolerance)
    paths=[]
    while edges:
        start=next(iter(edges)); points=[start]; p=start
        while True:
            q=edges[p].pop()
            if not edges[p]:del edges[p]
            points.append(q); p=q
            if p==start:break
        if len(points)<4:continue
        # Split the closed ring before RDP so its two endpoints do not coincide.
        split=max(range(1,len(points)-1),key=lambda i:math.dist(start,points[i]))
        points=simplify(points[:split+1])[:-1]+simplify(points[split:])
        fmt=lambda point: f'{point[0]*side/w:.2f} {point[1]*side/h:.2f}'
        paths.append('M'+fmt(points[0])+'L'+'L'.join(fmt(p) for p in points[1:])+'Z')
    return ''.join(paths)


def tint_heads(asset):
    cache={}; count=0
    def raster(markup, f):
        side=f['box'][2]; size=math.ceil(side*4)
        for i,level in enumerate(f['levels']):
            for route in ['m','o']:
                markup=markup.replace(f'var(--{route}{i})',f'rgb(0,{int(level*255+.5)},0)')
        markup=markup.replace('{id}','mask-')
        svg=f'<svg xmlns="http://www.w3.org/2000/svg" width="{size}" height="{size}" viewBox="0 0 {side} {side}">{markup}</svg>'
        key=hashlib.sha256(svg.encode()).hexdigest()
        if key not in cache:
            raw=subprocess.run(['rsvg-convert'],input=svg.encode(),stdout=subprocess.PIPE,check=True).stdout
            cache[key]=Image.open(io.BytesIO(raw)).convert('RGBA')
        return cache[key]
    for f in asset['fighters'].values():
        for src in [f]+list(f.get('outfits',{}).values()):
            for gear, heads in src.pop('tint_gear',{}).items():
                if gear == 'madhouse-hat': continue  # its approved authored colors stay intact
                common = src.get('gear',{}).get(gear) or (src.get('hat') if gear == 'pirate-hat' else None)
                if heads != common: src.setdefault('neutral_gear',{})[gear] = heads
                paths=[]
                for i,markup in enumerate(heads):
                    new,old=raster(markup,f),raster(src['head'][i],f)
                    # Native compares premultiplied RGBA with a 2/255 tolerance.
                    a=list(new.split());b=list(old.split())
                    for channel in range(3):
                        a[channel]=ImageChops.multiply(a[channel],a[3]);b[channel]=ImageChops.multiply(b[channel],b[3])
                    delta=Image.new('L',new.size)
                    for x,y in zip(a,b):delta=ImageChops.lighter(delta,ImageChops.difference(x,y))
                    mask=ImageChops.multiply(delta.point(lambda v:255 if v>2 else 0),a[3].point(lambda v:255 if v else 0))
                    paths.append(contours(mask,f['box'][2]))
                    count+=1
                src.setdefault('tint_masks',{})[gear]=paths
    return count


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--fgc',type=Path,required=True);parser.add_argument('--asset',type=Path,default=Path('internal/server/static/board-avatars.json'))
    args=parser.parse_args();asset=json.loads(args.asset.read_text());asset['recipes']=native_recipes(args.fgc)
    count=tint_heads(asset)
    args.asset.write_text(json.dumps(asset,separators=(',',':'),sort_keys=True)+'\n')
    print(f'Baked {len(asset["recipes"]["skins"])} skin recipes and {count} SVG head masks; {args.asset.stat().st_size} bytes, no raster payloads.')

if __name__=='__main__':main()
