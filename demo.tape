# Records demo.gif for the README: focus session + visualizer, task view,
# weekly heatmap. Uses a sandboxed Taskwarrior (TASKRC/TASKDATA) and a seeded
# ~/.understory.json — see the prep steps below. Regenerate with:
#   go build -o understory .
#   ffmpeg -y -f lavfi -i "sine=frequency=180:duration=25" \
#     -f lavfi -i "sine=frequency=523:duration=25" \
#     -f lavfi -i "sine=frequency=1320:duration=25" \
#     -f lavfi -i "anoisesrc=color=pink:duration=25:amplitude=0.12" \
#     -filter_complex "[0]tremolo=f=0.6:d=0.9[a];[1]tremolo=f=1.4:d=0.9[b];[2]tremolo=f=2.3:d=0.9[c];[a][b][c][3]amix=inputs=4:normalize=1" \
#     -ar 48000 /tmp/understory-chirp.wav
#   printf 'data.location=/tmp/demo-taskdata\nverbose=nothing\nconfirmation=off\n' > /tmp/demo.taskrc
#   TASKRC=/tmp/demo.taskrc TASKDATA=/tmp/demo-taskdata task add ... (a few demo tasks)
#   mv ~/.understory.json /tmp/state-backup.json   # seed a demo one, restore after
#   vhs demo.tape
# Audio plays out loud for ~25s.
Output demo.gif

Set FontSize 16
Set Width 1120
Set Height 768
Set Padding 16

Hide
Type "unset CI NO_COLOR; export COLORTERM=truecolor TASKRC=/tmp/demo.taskrc TASKDATA=/tmp/demo-taskdata"
Enter
Type "afplay /tmp/understory-chirp.wav &"
Enter
Type "clear"
Enter
Show

Type "./understory"
Sleep 500ms
Enter
Sleep 1.5s
Space
Sleep 1s
Type "v"
Sleep 5s
Type "t"
Sleep 3.5s
Escape
Sleep 500ms
Type "g"
Sleep 3.5s
Escape
Sleep 4s
