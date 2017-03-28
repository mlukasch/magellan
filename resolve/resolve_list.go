package resolve

import (
	"fmt"
	"reflect"

	"github.com/graphql-go/graphql/language/ast"
)

type listResolver struct {
	isPtr        bool
	elemResolver Resolver
}

func (lr *listResolver) Execute(rc *resolutionContext, resolver reflect.Value) {
	if lr.isPtr {
		if resolver.IsNil() {
			rc.SetValue(nil)
			return
		}
		resolver = resolver.Elem()
	}

	count := resolver.Len()
	if count == 0 {
		// Send a [] to fill the field.
		rc.SetValue(make([]string, 0))
		return
	}

	for i := 0; i < count; i++ {
		iv := resolver.Index(i)
		child := rc.Child(rc.qnode, true, false)
		child.SetArrayIndex(i)
		if rc.isSerial {
			lr.elemResolver.Execute(child, iv)
		} else {
			go lr.elemResolver.Execute(child, iv)
		}
	}
}

type chanListResolver struct {
	*listResolver
}

func (fr *chanListResolver) Execute(rc *resolutionContext, resolver reflect.Value) {
	if resolver.IsNil() {
		return
	}
	go func() {
		done := rc.ctx.Done()
		doneVal := reflect.ValueOf(done)
		idx := 0
		for {
			// resolver = <-chan string
			// select {
			chosen, recv, recvOk := reflect.Select([]reflect.SelectCase{
				// case rval := <-resolver:
				{
					Chan: resolver,
					Dir:  reflect.SelectRecv,
				},
				// case <-ctx.Done()
				{
					Chan: doneVal,
					Dir:  reflect.SelectRecv,
				},
			})
			switch chosen {
			case 0:
				if !recvOk {
					return
				}
				child := rc.Child(rc.qnode, true, false)
				child.SetArrayIndex(idx)
				go fr.elemResolver.Execute(child, recv)
				idx++
				continue
			case 1:
				return
			}
		}
	}()
}

func (rt *ResolverTree) buildListResolver(pair TypeResolverPair, ldef *ast.List) (Resolver, error) {
	rtType := pair.ResolverType
	rtKind := rtType.Kind()
	isPtr := rtKind == reflect.Ptr
	if isPtr {
		rtType = pair.ResolverType.Elem()
		rtKind = rtType.Kind()
	}
	isChan := rtKind == reflect.Chan
	if isChan {
		if rt.SerialOnly {
			return nil, fmt.Errorf("Cannot accept non-immediate result in mutations (at %s - mutations cannot return deferred values).", pair.ResolverType.String())
		}
		if rtType.ChanDir() != reflect.RecvDir {
			return nil, fmt.Errorf("Invalid array type %s, (should be a %v, is a %v)", pair.ResolverType.String(), reflect.RecvDir, pair.ResolverType.ChanDir())
		}
	} else {
		if rtKind != reflect.Slice {
			return nil, fmt.Errorf("Expected array type, got %v (should be a slice or a chan).", pair.ResolverType.String())
		}
	}

	// The type in the pair will be a []*ResolverType or []ResolverType or <-chan ResolverType etc...
	arrElem := rtType.Elem()
	if arrElem.Kind() == reflect.Ptr {
		arrElem = arrElem.Elem()
	}

	var cres *chanListResolver
	res := &listResolver{isPtr: isPtr}
	if isChan {
		cres = &chanListResolver{listResolver: res}
		rt.Resolvers[pair] = cres
	} else {
		rt.Resolvers[pair] = res
	}

	// Follow list element
	elemResolver, err := rt.buildFollowResolver(arrElem, ldef.Type)
	if err != nil {
		return nil, err
	}
	res.elemResolver = elemResolver

	if isChan {
		return cres, nil
	}

	return res, nil
}
