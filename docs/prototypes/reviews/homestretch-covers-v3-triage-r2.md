# Covers v3 round 2 triage (last round)

Fixed: #1 (long wait with a free slot now says it is not starting and to check the queue is paused), #2 (Queue page shows a "Queue running" state next to Pause), #3 (low disk and offline give a step; "See machine" is hidden on the Machines page), #4 (machine problem sits in its own labelled strip, apart from the branch headline), #5 (Queue card says offline instead of slots busy), #8 (next free slot shown on Queue and Machines pages), #9 (run page says why a stopped run stopped).

Rejected:
- #6: GitHub really gets "failed" on every covered change; the card already says we cannot tell which broke it.
- #7: the real app opens the job on GitHub; prototype-only note is the known convention.
- #10, #11: inert controls and stub tabs are prototype conventions, marked "Prototype only".
- #12, #13, #14, #17, #18: existing real dashboard wording, columns and focus ring, outside this feature.
- #15: the real mobile CSS rules, kept on purpose.
- #16: already cut to one place in the Ready state; the repeat in other states is the point of the Queue card.
