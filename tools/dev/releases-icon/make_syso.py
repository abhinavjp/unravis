import struct,sys
ico=open('releases.ico','rb').read()
_,_,n=struct.unpack('<HHH',ico[:6])
imgs=[]
for i in range(n):
    w,h,c,_,pl,bc,size,off=struct.unpack('<BBBBHHII',ico[6+16*i:22+16*i])
    imgs.append((w,h,c,pl,bc,ico[off:off+size]))
def al(b,n=4): return b+b'\0'*((-len(b))%n)
# directory layout: root(types) -> type dirs -> id dirs -> lang dirs -> data entries -> data
def dirhdr(n_id): return struct.pack('<IIHHHH',0,0,0,0,0,n_id)
def dent(id_,off,sub): return struct.pack('<II',id_,off|(0x80000000 if sub else 0))
types=[(3,[(i+1,imgs[i][5]) for i in range(n)]),(14,[(1,None)])]
grp=struct.pack('<HHH',0,1,n)+b''.join(struct.pack('<BBBBHHIH',w if w<256 else 0,h if h<256 else 0,c,0,pl,bc,len(d),i+1) for i,(w,h,c,pl,bc,d) in enumerate(imgs))
types[1]=(14,[(1,grp)])
# compute sizes
root_sz=16+8*len(types)
tdir_sz=[16+8*len(ids) for _,ids in types]
iddir_sz=16+8   # one language each
total_ids=sum(len(ids) for _,ids in types)
off=root_sz; tdir_off=[]
for s in tdir_sz: tdir_off.append(off); off+=s
iddir_off=[]
for _ in range(total_ids): iddir_off.append(off); off+=iddir_sz
data_entry_off=[]
for _ in range(total_ids): data_entry_off.append(off); off+=16
raw_off=off
out=bytearray(); relocs=[]
out+=dirhdr(len(types))
for (t,ids),to in zip(types,tdir_off): out+=dent(t,to,True)
for (t,ids) in types:
    out+=dirhdr(len(ids))
k=0
# type dirs hold id entries
out=bytearray(); out+=dirhdr(len(types))
for (t,ids),to in zip(types,tdir_off): out+=dent(t,to,True)
for (t,ids) in types:
    out+=dirhdr(len(ids))
    for (i,_) in ids:
        out+=dent(i,iddir_off[k],True); k+=1
k=0
for (t,ids) in types:
    for _ in ids:
        out+=dirhdr(1)+dent(0x409,data_entry_off[k],False); k+=1
datas=[]; k=0; cur=raw_off
for (t,ids) in types:
    for (i,d) in ids:
        relocs.append(len(out)); out+=struct.pack('<IIII',cur,len(d),0,0)
        datas.append(al(d)); cur+=len(al(d)); k+=1
assert len(out)==raw_off,(len(out),raw_off)
for d in datas: out+=d
sec=bytes(out)
sym=b'$R000000'+struct.pack('<IhHBB',0,1,0,3,0)
hdr_sz=20+40; ptr_reloc=hdr_sz+len(sec); ptr_sym=ptr_reloc+10*len(relocs)
f=struct.pack('<HHIIIHH',0x8664,1,0,ptr_sym,1,0,0)
f+=b'.rsrc\0\0\0'+struct.pack('<IIIIIIHHI',0,0,len(sec),hdr_sz,ptr_reloc,0,len(relocs),0,0xC0000040)
f+=sec+b''.join(struct.pack('<IIH',r,0,3) for r in relocs)+sym+struct.pack('<I',4)
open('rsrc_windows_amd64.syso','wb').write(f); print(len(f),'bytes',total_ids,'resources')
