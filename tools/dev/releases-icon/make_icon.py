from PIL import Image, ImageDraw
S=1024
def base():
    im=Image.new('RGBA',(S,S),(0,0,0,0)); d=ImageDraw.Draw(im)
    d.ellipse((24,24,S-24,S-24),fill=(232,131,17,255))
    w=(255,255,255,255)
    d.polygon([(512,215),(790,520),(620,520),(620,680),(404,680),(404,520),(234,520)],fill=w)
    d.rounded_rectangle((300,745,724,830),radius=30,fill=w)
    return im
big=base()
sizes=[16,24,32,48,64,128,256]
imgs=[big.resize((s,s),Image.LANCZOS) for s in sizes]
imgs[-1].save('releases.ico',format='ICO',sizes=[(s,s) for s in sizes],append_images=imgs[:-1])
prev=Image.new('RGBA',(520,300),(247,247,245,255)); x=10
for s in (256,64,32,16):
    i=big.resize((s,s),Image.LANCZOS); prev.paste(i,(x,10),i); x+=s+20
dark=Image.new('RGBA',(520,150),(20,20,19,255)); x=10
for s in (64,32,16):
    i=big.resize((s,s),Image.LANCZOS); dark.paste(i,(x,10),i); x+=s+20
u=Image.open('/tmp/unravis-icon.png').convert('RGBA')
for k,s in enumerate((64,32,16)):
    i=u.resize((s,s),Image.LANCZOS); dark.paste(i,(300+k*80,10),i)
prev.paste(dark,(0,150)); prev.save('preview.png')
