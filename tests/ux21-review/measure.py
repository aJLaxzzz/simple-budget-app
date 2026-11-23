# Read PNG pixels without modifying any original; coordinates in screenshot pixels.
from pathlib import Path
import struct,zlib,json
R=Path('artifacts/ux21-final-verification')
def read(p):
 b=p.read_bytes();at=8;chunks=[]
 while at<len(b):
  size=struct.unpack('>I',b[at:at+4])[0];kind=b[at+4:at+8];data=b[at+8:at+8+size];at+=size+12
  if kind==b'IHDR':w,h,depth,color,*_=struct.unpack('>IIBBBBB',data)
  if kind==b'IDAT':chunks.append(data)
 assert depth==8 and color in [2,6]
 n=3 if color==2 else 4;stride=w*n;raw=zlib.decompress(b''.join(chunks));rows=[];prev=bytearray(stride);at=0
 for y in range(h):
  f=raw[at];at+=1;row=bytearray(raw[at:at+stride]);at+=stride
  for i in range(stride):
   a=row[i-n] if i>=n else 0;u=prev[i];ul=prev[i-n] if i>=n else 0
   if f==1:v=a
   elif f==2:v=u
   elif f==3:v=(a+u)//2
   elif f==4:
    q=a+u-ul;pa,pb,pc=abs(q-a),abs(q-u),abs(q-ul);v=a if pa<=pb and pa<=pc else u if pb<=pc else ul
   else:v=0
   row[i]=(row[i]+v)&255
  rows.append(row);prev=row
 return lambda x,y:tuple(rows[y][x*n:x*n+3])
results=[]
for id,folder,start,end,x in [('tasks-twenty','02-tasks',550,690,700),('calendar-month-dense','04-calendar',480,550,700)]:
 old=Path('artifacts/final-ux-review/screenshots')/folder/(id+'-1440.png');get=read(old)
 runs=[];last=None
 for y in range(start,end):
  col=get(x,y)
  if col!=last:runs.append({'y':y,'rgb':col});last=col
 results.append({'scenario':id,'old_scan_x':x,'old_scan_range':[start,end],'pixel_runs':runs})
 print(id,runs)
for id,folder,start,end,x in [('tasks-twenty','02-tasks',310,370,700),('calendar-month-dense','04-calendar',320,360,700)]:
 get=read(R/'after-screenshots'/folder/(id+'-1440.png'));runs=[];last=None
 for y in range(start,end):
  col=get(x,y)
  if col!=last:runs.append({'y':y,'rgb':col});last=col
 results.append({'scenario':id,'version':'after','scan_x':x,'range':[start,end],'pixel_runs':runs});print(id,'after',runs)
(R/'checks/header-pixel-measurements.json').write_text(json.dumps(results,indent=2))
