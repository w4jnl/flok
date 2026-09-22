# The README recording. `assets/demo/record.sh` builds the scene (three fake agents on isolated
# tmux servers), plays this tape with vhs in a temporary directory (vhs writes nothing when its
# working directory is on a secondary volume) and assembles assets/demo.gif from the frames
# with ffmpeg (vhs 0.12's own encoder step is silent and empty with ffmpeg 9).
Output frames/
Set Shell bash
Set FontSize 14
Set Width 1200
Set Height 680
Set Padding 8
Set Theme "Dracula"
Set Framerate 20
Set TypingSpeed 60ms

Hide
Type "tmux -L demo-outer attach" Enter
Sleep 2s
Show
Sleep 10s
# ~8 s in, api asks for permission (timeline in record.sh)
Ctrl+b
Type "o"
Sleep 3s
Type "1" Enter
Sleep 8s
