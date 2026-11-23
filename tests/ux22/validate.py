from pathlib import Path
from html.parser import HTMLParser
from urllib.parse import urlsplit,unquote
import json,hashlib,struct,zlib,zipfile,tempfile,subprocess,re
R=Path('artifacts/ux22-final').resolve();root=Path.cwd();m=json.loads((R/'manifest.json').read_text())
assert len(m)==46 and len({e['id'] for e in m})==46
for p,h in json.loads((R/'checks/before-images-sha256.json').read_text()).items():assert hashlib.sha256((R/p).read_bytes()).hexdigest()==h
sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()
old=json.loads((R/'checks/source-before.json').read_text());changed=[p for p,h in old.items() if sha(root/p)!=h]
assert set(changed)==set(json.loads((R/'checks/product-changed.json').read_text()))
assert all(p.startswith('static/') and not p.startswith('static/vendor/') for p in changed)
class Links(HTMLParser):
 def __init__(self):super().__init__();self.urls=[]
 def handle_starttag(self,t,a):self.urls.extend(v for k,v in a if k in ('src','href'))
links=0
for p in [R/'index.html',*sorted((R/'contact-sheets').glob('*.html'))]:
 parser=Links();parser.feed(p.read_text())
 for url in parser.urls:
  u=urlsplit(url);assert not u.scheme and not u.netloc,(p,url)
  if not u.path:continue
  dst=(p.parent/unquote(u.path)).resolve();assert dst.is_relative_to(R) and dst.is_file(),(p,url);links+=1
for x in re.findall(r'\]\(([^)]+)\)',(R/'UX22-REPORT.md').read_text()):assert (R/x).is_file(),x
counts={};sizes={}
for category in ['before','after','comparisons','contact-sheets']:
 files=list((R/category).rglob('*.png'));counts[category]=len(files)
 for p in files:
  b=p.read_bytes();assert b[:8]==b'\x89PNG\r\n\x1a\n';offset=8;parts=[]
  while offset<len(b):
   n=struct.unpack('>I',b[offset:offset+4])[0];kind=b[offset+4:offset+8];data=b[offset+8:offset+8+n];crc=struct.unpack('>I',b[offset+8+n:offset+12+n])[0];assert zlib.crc32(kind+data)&0xffffffff==crc,p
   if kind==b'IDAT':parts.append(data)
   offset+=n+12
   if kind==b'IEND':break
  assert zlib.decompress(b''.join(parts));sizes[str(p.relative_to(R))]=struct.unpack('>II',b[16:24])
assert counts==dict(before=46,after=46,comparisons=46,**{'contact-sheets':5})
for e in m:
 v=e['viewport'];assert sizes[e['before']]==sizes[e['after']]==(v['width'],v['height']);assert sizes[e['comparison']]==(v['width']*2,v['height']+60)
 e['sha256']={k:sha(R/e[k]) for k in ['before','after','comparison']}
assert all(not e['metrics']['after']['overflow'] for e in m)
(R/'manifest.json').write_text(json.dumps(m,ensure_ascii=False,indent=2))
logs=['go.log','race.log','vet.log','build.log','unit.log','playwright-full-final.log','before.log','after.log','gallery.log','diff-check.log','source-before.json','product-changed.json','before-images-sha256.json','calendar-group-final.log','ux22-final.log']
report=(R/'UX22-REPORT.md').read_text();assert 'ОЖИДАЕТ' not in report
result={'scenario_pairs':46,'png_counts':counts,'png_crc_and_deflate':'valid','local_links_checked':links,'horizontal_overflow':0,'unchanged_source_files':len(old)-len(changed),'product_changed':changed,'zip_crc':'valid','sqlite_or_financial_data_in_archive':False,'extracted_gallery':'file://; images decoded; no network requests or pageerrors'}
(R/'checks/integrity.json').write_text(json.dumps(result,ensure_ascii=False,indent=2))
files=[R/name for name in ['index.html','manifest.json','before-manifest.json','after-manifest.json','UX22-REPORT.md']]+[p for cat in counts for p in (R/cat).rglob('*') if p.is_file()]+[R/'checks'/name for name in logs+['integrity.json']]
archive=R.parent/'ux22-final.zip'
with zipfile.ZipFile(archive,'w',zipfile.ZIP_DEFLATED,compresslevel=6)as z:
 for p in sorted(files):
  assert p.suffix not in ['.db','.sqlite','.sqlite3'] and p.name not in ['data.json','categories.json']
  z.write(p,str(Path(R.name)/p.relative_to(R)))
with zipfile.ZipFile(archive)as z:
 assert z.testzip() is None
 for p in files:assert hashlib.sha256(z.read(str(Path(R.name)/p.relative_to(R)))).hexdigest()==sha(p)
 with tempfile.TemporaryDirectory(prefix='ux22-gallery-check-')as temp:
  z.extractall(temp)
  script="""const {chromium}=require('@playwright/test');(async()=>{const b=await chromium.launch({channel:'chrome'}),p=await b.newPage();const errors=[],external=[];p.on('pageerror',e=>errors.push(e.message));p.on('request',r=>{if(/^https?:/.test(r.url()))external.push(r.url())});await p.goto('file://'+process.argv[1]+'/ux22-final/index.html');await p.locator('img').evaluateAll(xs=>xs.forEach(x=>x.loading='eager'));await p.evaluate(()=>Promise.all([...document.images].map(i=>i.decode())));if(await p.locator('article').count()!==46||errors.length||external.length)throw Error(JSON.stringify({errors,external}));await p.getByRole('button',{name:'До',exact:true}).click();if(await p.locator('.side-after:visible').count())throw Error('Before toggle');await p.getByRole('button',{name:'Рядом',exact:true}).click();await p.locator('#search').fill('уборка');if(!await p.locator('article:not([hidden])').count())throw Error('Search');await b.close();console.log('Extracted gallery passed');})()"""
  subprocess.run(['node','-e',script,temp],check=True)
archive.with_suffix('.zip.sha256').write_text(sha(archive)+'  '+archive.name+'\n')
print(json.dumps(result,ensure_ascii=False,indent=2));print('ZIP bytes',archive.stat().st_size,'files',len(files))
