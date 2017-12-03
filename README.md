#httpLogger

A simple server to log oil burner run and temperature data from HTTP requests.

This runs on a file server in my house, keeping log info from a Raspberry Pi in the
basement, which tracks various stuff (including the oil burner).

When it receives an HTTP GET request on /temps/run, it starts recording temperature
entries from sensors around the house every 2 minutes. It continues to do so until after
the oil burner shuts off, and the number of entries post-run can be configured. It also
stores the run information it receives as an HTTP POST on /burnerlogger.

This was written in part as an exercise in learning Golang, and for a really specific use-case.
I don't expect it's very good Golang, but it works, and if it can be of any use to anyone, help yourself...

The config file must be in the same directory as the executable.
"httploggerport" is the port the server listens on.
"temperatures" is the URL to a tiny server on the Pi that gathers temperature sensor info and sends it in a
JSON blob, such as:
```
{
  "basement": 55.60,
  "bed": 60.24,
  "crawlspace": 44.15,
  "downstairs": 60.35,
  "garage": 36.61,
  "upstairs": 68.11
}
```

The "partner" monitoring daemon is at: https://github.com/MaineTim/burnerWatcher
