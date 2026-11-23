import pathlib,json,re,hashlib,struct,zipfile
from html.parser import HTMLParser
R=pathlib.Path('artifacts/final-ux-review');m=json.loads((R/'manifest.json').read_text());seen=set();files=set();fail=[]
for x in m:
 if x['id'] in seen:fail.append('Duplicate scenario '+x['id'])
 seen.add(x['id'])
 for k in ['file','full_page_file']:
  if not x.get(k):continue
  p=R/x[k];files.add(p)
  if not p.exists():fail.append('Missing '+str(p));continue
  b=p.read_bytes()
  if b[:8]!=b'\x89PNG\r\n\x1a\n':fail.append('Invalid PNG '+str(p));continue
  w,h=struct.unpack('>II',b[16:24])
  if k=='file' and (w!=x.get('viewport',{}).get('width',1440) or h!=900):fail.append('Unexpected dimensions '+str(p)+': '+str((w,h)))
  if k=='file':x['sha256']=hashlib.sha256(b).hexdigest()
class Links(HTMLParser):
 def __init__(self):super().__init__();self.paths=[]
 def handle_starttag(self,tag,attrs):
  for k,v in attrs:
   if k in ('href','src') and v and not v.startswith(('#','data:')):self.paths.append(v)
for p in R.rglob('*.html'):
 q=Links();q.feed(p.read_text())
 for link in q.paths:
  if '://' in link:fail.append('External gallery dependency '+link)
  elif not (p.parent/link.split('#')[0]).exists():fail.append('Broken '+str(p)+' -> '+link)
for link in re.findall(r'\]\(([^)]+)\)',(R/'UX-REVIEW.md').read_text()):
 if '://' not in link and not (R/link.split('#')[0]).exists():fail.append('Broken report link '+link)
for p in (R/'contact-sheets').glob('sheet-*.html'):
 n=p.read_text().count('class="card"')
 if not 12<=n<=16:fail.append('Invalid contact sheet count '+str(p)+': '+str(n))
orphans=set((R/'screenshots').rglob('*.png'))-files
if orphans:fail.append('Unlisted screenshots: '+str(sorted(map(str,orphans))))
before=json.loads((R/'checks/product-before.json').read_text());changed=[]
for name,sha in before.items():
 p=pathlib.Path(name)
 if not p.exists() or hashlib.sha256(p.read_bytes()).hexdigest()!=sha:changed.append(name)
if changed:fail.append('Product files changed: '+str(changed))
(R/'manifest.json').write_text(json.dumps(m,ensure_ascii=False,indent=2))
result={'scenarios':len(m),'original_png':len(files),'contact_sheets':len(list((R/'contact-sheets').glob('*.png'))),'product_files_checked':len(before),'product_changed':changed,'broken_links_or_images':fail}
(R/'checks/package-validation.json').write_text(json.dumps(result,ensure_ascii=False,indent=2));print(json.dumps(result,ensure_ascii=False,indent=2))
if fail:raise SystemExit(1)
if '--zip' in __import__('sys').argv:
 target=pathlib.Path('artifacts/final-ux-review.zip')
 with zipfile.ZipFile(target,'w',zipfile.ZIP_DEFLATED,compresslevel=6) as z:
  for p in sorted(R.rglob('*')):
   if p.is_file():z.write(p,pathlib.Path('final-ux-review')/p.relative_to(R))
 with zipfile.ZipFile(target) as z:
  assert z.testzip() is None
  assert 'final-ux-review/index.html' in z.namelist()
 print('ZIP OK',target.stat().st_size,'bytes')
 pathlib.Path(str(target)+'.sha256').write_text(hashlib.sha256(target.read_bytes()).hexdigest()+'  final-ux-review.zip\n')
