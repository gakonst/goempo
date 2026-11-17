package rpc

func debugTraceCallMany(s *Server, p *params) ([]byte, error) {
	args, err := callBundlesArgs(s, p)
	if err != nil {
		return nil, err
	}
	bundles, n := args.bundles, args.block
	if len(bundles) == 0 {
		return nil, newError(CodeInvalidParams, "bundles are empty.")
	}
	opts, err := traceOptions(p)
	if err != nil {
		return nil, err
	}
	if s.exec == nil {
		return nil, internalError("unimplemented")
	}
	res, err := s.exec.TraceCallMany(n, bundles, opts)
	if err != nil {
		return nil, executionError(err)
	}
	return encode(func(w *writer) {
		w.arrOpen()
		for _, bundle := range res {
			w.arrOpen()
			for _, t := range bundle {
				w.raw(t)
			}
			w.arrClose()
		}
		w.arrClose()
	}), nil
}
