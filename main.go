package main

import "github.com/khan/fastlike/fastlike"

func main() {
	fastlike := fastlike.New("./bin/main.wasm")
	instance := fastlike.Instantiate()

	instance.Run()
	/*
		linker, module := prepare()
		instance, err := linker.Instantiate(module)
		check(err)

		wmemory := instance.GetExport("memory").Memory()
		fmt.Printf("memory size=%d\n", wmemory.Size())
		memory = WasmMemory{wmemory}

		entry := instance.GetExport("main2").Func()
		val, err := entry.Call()
		check(err)
		fmt.Printf("entry() = %+v\n", val)
	*/
}
