package scorecli

// midi.go — `foundry score midi`: the MIDI keyboards the editor can use,
// and what one sends, so a device that does not reach the editor can be
// checked from a terminal.

import (
	"flag"
	"fmt"
	"io"
	"time"

	"foundry/internal/mididev"
)

func midiCmd(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("midi", flag.ContinueOnError)
	fs.SetOutput(out)
	monitor := fs.Float64("monitor", 0, "print what the port sends for this many seconds")
	port := fs.String("port", "", "the port to monitor, by name (default: the first)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	ports, err := mididev.List()
	if err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	if len(ports) == 0 {
		fmt.Fprintln(out, "no MIDI input ports")
	}
	for _, p := range ports {
		fmt.Fprintf(out, "%-8s %s\n", p.ID, p.Name)
	}
	if *monitor <= 0 {
		return 0
	}
	var in *mididev.Input
	switch {
	case *port != "":
		in, err = mididev.OpenByName(*port)
	case len(ports) > 0:
		in, err = mididev.Open(ports[0].ID)
	default:
		return 1
	}
	if err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	defer in.Close()
	fmt.Fprintf(out, "listening to %s for %.0f s\n", in.Port.Name, *monitor)
	start := time.Now()
	var buf []mididev.Message
	for time.Since(start).Seconds() < *monitor {
		time.Sleep(10 * time.Millisecond)
		buf, err = in.Poll(buf[:0])
		for _, m := range buf {
			fmt.Fprintf(out, "%8.3f  %s\n", m.Time.Sub(start).Seconds(), m)
		}
		if err != nil {
			fmt.Fprintln(out, err)
			return 1
		}
	}
	return 0
}
