// +build js

package mvuex

import (
	"reflect"
	"github.com/gopherjs/gopherjs/js"
	//"github.com/gopherjs/jsbuiltin"
)


func wrapGoGetterFunc(reflectedGoFunc reflect.Value ) (jsFunc *js.Object, err error) {

	numGoArgs := reflectedGoFunc.Type().NumIn() //Number of arguments of the Go target method

	// ToDo: check if one or two IN arguments in Go function (state interface{}, getters *js.Object)


	goCallArgTargetTypes := make([]reflect.Type, numGoArgs)
	goCallArgsTargetValues := make([]reflect.Value,numGoArgs) //create call args slice, containing the store arg
	for i := 0; i < reflectedGoFunc.Type().NumIn(); i++ {
		goCallArgTargetTypes[i] = reflectedGoFunc.Type().In(i)
	}

	jsFunc = js.MakeFunc(func(this *js.Object, arguments []*js.Object) interface{} {

		//println("JS func called")

		//get method argument type at this poistion
		castedArg, err := castToType(goCallArgTargetTypes[0], arguments[0]) // ignore second argument (which includes getters and leads to endless recursive calls on access)
		if err != nil { panic("Error converting JS object to "  + goCallArgTargetTypes[0].Kind().String()) }
		goCallArgsTargetValues[0] = castedArg

		if numGoArgs > 1 {
			goCallArgsTargetValues[1] = reflect.ValueOf(arguments[1])
		}


		results := reflectedGoFunc.Call(goCallArgsTargetValues)

		// in case of multiple return values, convert to interface slice --> gets externalized as JS Array
		if len(results) == 1 {
			return results[0].Interface()
		} else {
			iResults := make([]interface{}, len(results))
			for i,res := range results {
				iResults[i] = res.Interface() // Convert reflect value, back to Go interface, which could be interpreted by gopherjs externalize
			}
			return iResults
		}
	})

	return jsFunc, nil
}

/*
Usage examples for the various use cases from https://vuex.vuejs.org/guide/getters.html

	store := mvuex.NewStore(
		mvuex.State(state),
		...

		mvuex.Getter("testgetterProperty", func(state *GlobalState) interface{} {
			//Note: GlobalState is a custom struct, used for the vuex store state
			println("getter returning a property, for given state", state)
			return state
		}),

		mvuex.Getter("testgetterPropertyMulti", func(state *GlobalState) (string, int) {
			println("getter returning a property with multiple results converted to an array, for given state", state)
			return "two", 2
		}),

		mvuex.Getter("testgetterMethodWithArg", func(state interface{}) interface{} {
			println("getter returning a function which takes an argument, input state isn't casted to known struct", state)
			return func(i int) int { return i * 2 } // function returning given int multiplied by two
		}),
		mvuex.Getter("testgetterConsumeGetters", func(state *GlobalState, getters *js.Object) interface{} {
			println("getter consuming state and getters as input", state)
			println("getter3 getters", getters)
			return getters
		}),

	)
*/
func Getter(name string, goFunc interface{}) StoreOption {
	return func(c *StoreConfig) {
		if c.Getters == js.Undefined { c.Getters = o() }

		reflectedGoFunc := reflect.ValueOf(goFunc)
		if reflectedGoFunc.Kind() != reflect.Func { //check if the provided interface is a go function
			panic("Getter " + name + " is not a func")
		}

		//try to convert the provided function to a JavaScript function usable as Mutation
		jsFunc, err := wrapGoGetterFunc(reflectedGoFunc)
		if err != nil {panic("Error exposing the getter function '"+ name + "' to JavaScript: " + err.Error())}

		c.Getters.Set(name, jsFunc)
		//c.Mutations.Set(name, makeMethod(name, false, reflectGoFunc.Type(), reflectGoFunc))
	}
}
