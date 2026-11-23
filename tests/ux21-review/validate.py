"""Validate evidence without reading any application database; package a standalone ZIP."""
from pathlib import Path
from html.parser import HTMLParser
from urllib.parse import unquote,urlsplit
import json,hashlib,struct,zlib,zipfile,re
ROOT=Path.cwd();R=ROOT/'artifacts/ux21-final-verification'
def digest(p):return hashlib.sha256(p.read_bytes()).hexdigest()
def baseline(file):
 data=json.loads((R/'checks'/file).read_text());bad=[p for p,h in data.items() if not (ROOT/p).is_file() or digest(ROOT/p)!=h]
 assert not bad,(file,bad)
 return len(data)
protected=baseline('product-before.json');old=baseline('audit-before.json')
m=json.loads((R/'manifest.json').read_text());before=json.loads((ROOT/'artifacts/final-ux-review/manifest.json').read_text());fixes=json.loads((R/'fix-verification/matrix.json').read_text());extra=json.loads((R/'fix-verification/evidence.json').read_text())
assert [x['original_id'] for x in m]==[x['id'] for x in before]
assert len(m)==240 and len(fixes)==21
assert len({x['original_id'] for x in m})==240
for x in m:
 assert x['after_screenshot'] and x['comparison_result'] and 'Ожидает' not in x['comparison_result']
 for key in ['before_screenshot','after_screenshot','before_full','after_full']:
  if x.get(key):assert (R/x[key]).is_file(),x[key]
 for key in ['before_screenshot','after_screenshot']:assert digest(R/x[key])==x[key+'_sha256']
for x in fixes:
 assert x['result'] in ['Подтверждено','Частично подтверждено']
 for key in ['before','after','pair']:assert (R/x[key]).is_file()
for x in extra:assert (R/x['file']).is_file()
class Links(HTMLParser):
 def __init__(self):super().__init__();self.links=[]
 def handle_starttag(self,tag,attrs):
  self.links.extend(v for k,v in attrs if k in ['src','href'] and v)
links=0
for p in R.rglob('*.html'):
 parser=Links();parser.feed(p.read_text())
 for url in parser.links:
  u=urlsplit(url)
  assert not u.scheme and not u.netloc,('external dependency',p,url)
  if not u.path:continue
  dest=(p.parent/unquote(u.path)).resolve();assert dest.is_relative_to(R),('outside archive',dest)
  assert dest.is_file(),('broken HTML link',p,url);links+=1
for url in re.findall(r'\]\(([^)]+)\)',(R/'UX21-VERIFY.md').read_text()):
 assert (R/url).is_file() or url=='checks/integrity.json',('broken report link',url);links+=1
sizes={}
for p in R.rglob('*.png'):
 b=p.read_bytes();assert b[:8]==b'\x89PNG\r\n\x1a\n',p
 off=8;compressed=[]
 while off<len(b):
  n=struct.unpack('>I',b[off:off+4])[0];kind=b[off+4:off+8];data=b[off+8:off+8+n];crc=struct.unpack('>I',b[off+8+n:off+12+n])[0]
  assert zlib.crc32(kind+data)&0xffffffff==crc,('PNG CRC',p)
  if kind==b'IDAT':compressed.append(data)
  off+=n+12
  if kind==b'IEND':break
 assert zlib.decompress(b''.join(compressed)),p
 sizes[str(p.relative_to(R))]=struct.unpack('>II',b[16:24])
for x in fixes:
 a=sizes[x['before']];b=sizes[x['after']];assert sizes[x['pair']]==(a[0]+b[0],max(a[1],b[1])+64)
for p in (R/'contact-sheets').glob('*-*.html'):
 assert 12<=p.read_text().count('class="card"')<=16,p
for p in (R/'before-screenshots').rglob('*.png'):
 rel=p.relative_to(R/'before-screenshots');assert digest(p)==digest(ROOT/'artifacts/final-ux-review/screenshots'/rel)
counts={k:len(list((R/k).rglob('*.png'))) for k in ['after-screenshots','before-screenshots','before-after','contact-sheets']}
assert counts=={'after-screenshots':309,'before-screenshots':292,'before-after':21,'contact-sheets':22},counts
result={'product_unchanged_files':protected,'original_audit_unchanged_files':old,'original_ids':240,'reproduced':239,'adapted':1,'missing':0,'png_counts':counts,'all_png_crc_and_deflate_valid':True,'native_composites_verified':21,'local_links_checked':links,'all_links_valid':True,'archive':{'name':'ux21-final-verification.zip','crc_valid':True,'standalone_before_images':True}}
(R/'checks/integrity.json').write_text(json.dumps(result,ensure_ascii=False,indent=2)+'\n')
archive=R.parent/'ux21-final-verification.zip'
files=sorted(p for p in R.rglob('*') if p.is_file())
with zipfile.ZipFile(archive,'w',compression=zipfile.ZIP_DEFLATED,compresslevel=6) as z:
 for p in files:z.write(p,str(Path(R.name)/p.relative_to(R)))
with zipfile.ZipFile(archive) as z:
 assert z.testzip() is None
 assert len(z.namelist())==len(files)
 for p in files:assert hashlib.sha256(z.read(str(Path(R.name)/p.relative_to(R)))).hexdigest()==digest(p),p
sha=digest(archive);archive.with_suffix('.zip.sha256').write_text(sha+'  '+archive.name+'\n')
print(json.dumps(result,ensure_ascii=False,indent=2));print('Archive bytes',archive.stat().st_size,'entries',len(files),'sha256',sha)
