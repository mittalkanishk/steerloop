#### What a manifest is ? 
A tool manifest is a YAML file listing the tools the model may call. Right now, each tool is a CLI program. examples/tool.yaml is an example of such a manifest file. 

```json
 - name: count_lines
    description: Count the lines of a file in the current directory.
    parameters:                      # JSON Schema: the arguments the model must send
      type: object
      properties:
        path: { type: string }
      required: [path]
    command: ["wc", "-l", "--", "{{.path}}"]   # argv template, never run through a shell
    confirm: true                    # optional: ask or reconfirm from user before running
```