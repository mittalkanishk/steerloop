### The Very Basics - 
What is Channels and what is a goroutine , link any public video link to it.

### Testify/suite.
Testify suite and the *testing.T object, understanding the testing.T 
Testify is the name of the library in golang that I have seen developers use in production and most of the test suits that I write are in the same format as it makes it much cleaner to understand the behaviour of any kind of ai generated code or even end to end features that you want to take a deep dive in, the tests are the main thing, but i would like to know you opinions on were you able to visualize what i was trying to show you or not ? You can let in the comments below !

Run finds every method on the suite whose name starts with Test and runs each one as a subtest of t. TestCLISuite calls it with suite.Run(t, new(CLISuite)) and underneath uses a regex to find and decipher names of the tests.

Each defined test method is wrapped, before the call, testify calls SetupTest if you defined it. After the call it runs TearDownTest, restores the parent T and turns a panic into a test failure.

SetupSuite runs once before the whole list and TearDownSuite once after. It then executes the list with `t.Run`, So the names you see are `TestCLISuite/TestRun_GoldenJSONL`... and the rest.

*testing.T is the handle, Go's test runner gives one test. Failure, logging, and subtests all go through it: `t.Error`, `t.Fatal`, `t.Helper`, `t.Run`, `t.TempDir`. suite.SetT(t) stores that handle on the suite before each method runs. s.Require() and s.Equal then report to the same T, so a failure is attached to TestCLISuite/TestRun_GoldenJSONL and not to the parent. When the method returns, testify puts the parent T back.


### io.Pipe - 

io.Pipe connect a writer to a reader when the bytes are produced as you go, not all at once. A `bytes.buffer` only works after the write is finished. A pipe lets one goroutine write while another reads.

Typical Uses: 

- Feed a stream into an API that wants an io.Reader:  compress, hash, or upload while something else is still generating the bytes.

- Connect two stages, the way a shell connects cmd1 | cmd2 , without a real OS pipe.

- Tests that pretend to be a person at a keyboard. That is the usecase what this repo does in typing: the command reads pr i.e. `pipereader`, and the test writes the reply on pw `pipewriter` only after the prompt shows up on stderr pw.Close() is the Clrl-D.

- Use a buffer when you already have the whole payload. Use a pipe when the reader must start before the writer is done.



