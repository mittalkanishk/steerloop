### io.Pipe - 

io.Pipe connect a writer to a reader when the bytes are produced as you go, not all at once. A `bytes.buffer` only works after the write is finished. A pipe lets one goroutine write while another reads.

Typical Uses: 

- Feed a stream into an API that wants an io.Reader:  compress, hash, or upload while something else is still generating the bytes.

- Connect two stages, the way a shell connects cmd1 | cmd2 , without a real OS pipe.

- Tests that pretend to be a person at a keyboard. That is the usecase what this repo does in typing: the command reads pr i.e. `pipereader`, and the test writes the reply on pw `pipewriter` only after the prompt shows up on stderr pw.Close() is the Clrl-D.

- Use a buffer when you already have the whole payload. Use a pipe when the reader must start before the writer is done.



