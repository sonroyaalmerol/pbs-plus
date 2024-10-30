package targets

import (
	"encoding/json"
	"net/http"

	"sgl.com/pbs-ui/store"
	"sgl.com/pbs-ui/utils"
)

func D2DTargetHandler(storeInstance *store.Store) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Invalid HTTP method", http.StatusBadRequest)
		}

		all, err := storeInstance.GetAllTargets()
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}

		digest, err := utils.CalculateDigest(all)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}

		toReturn := TargetsResponse{
			Data:   all,
			Digest: digest,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(toReturn)
	}
}

func ExtJsTargetHandler(storeInstance *store.Store) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		response := TargetConfigResponse{}
		if r.Method != http.MethodPost {
			http.Error(w, "Invalid HTTP method", http.StatusBadRequest)
		}

		w.Header().Set("Content-Type", "application/json")

		err := r.ParseForm()
		if err != nil {
			response.Message = err.Error()
			response.Status = http.StatusBadGateway
			response.Success = false
			json.NewEncoder(w).Encode(response)
			return
		}

		newTarget := store.Target{
			Name: r.FormValue("name"),
			Path: r.FormValue("path"),
		}

		err = storeInstance.CreateTarget(newTarget)
		if err != nil {
			response.Message = err.Error()
			response.Status = http.StatusBadGateway
			response.Success = false
			json.NewEncoder(w).Encode(response)
			return
		}

		response.Status = http.StatusOK
		response.Success = true
		json.NewEncoder(w).Encode(response)
	}
}

func ExtJsTargetSingleHandler(storeInstance *store.Store) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		response := TargetConfigResponse{}
		if r.Method != http.MethodPut && r.Method != http.MethodGet && r.Method != http.MethodDelete {
			http.Error(w, "Invalid HTTP method", http.StatusBadRequest)
		}

		w.Header().Set("Content-Type", "application/json")

		if r.Method == http.MethodPut {
			target, err := storeInstance.GetTarget(r.PathValue("target"))
			if err != nil {
				response.Message = err.Error()
				response.Status = http.StatusBadGateway
				response.Success = false
				json.NewEncoder(w).Encode(response)
				return
			}

			err = r.ParseForm()
			if err != nil {
				response.Message = err.Error()
				response.Status = http.StatusBadGateway
				response.Success = false
				json.NewEncoder(w).Encode(response)
				return
			}

			if r.FormValue("name") != "" {
				target.Name = r.FormValue("name")
			}
			if r.FormValue("path") != "" {
				target.Path = r.FormValue("path")
			}

			if delArr, ok := r.Form["delete"]; ok {
				for _, attr := range delArr {
					switch attr {
					case "name":
						target.Name = ""
					case "path":
						target.Path = ""
					}
				}
			}

			err = storeInstance.UpdateTarget(*target)
			if err != nil {
				response.Message = err.Error()
				response.Status = http.StatusBadGateway
				response.Success = false
				json.NewEncoder(w).Encode(response)
				return
			}

			response.Status = http.StatusOK
			response.Success = true
			json.NewEncoder(w).Encode(response)

			return
		}

		if r.Method == http.MethodGet {
			target, err := storeInstance.GetTarget(r.PathValue("target"))
			if err != nil {
				response.Message = err.Error()
				response.Status = http.StatusBadGateway
				response.Success = false
				json.NewEncoder(w).Encode(response)
				return
			}

			response.Status = http.StatusOK
			response.Success = true
			response.Data = target
			json.NewEncoder(w).Encode(response)

			return
		}

		if r.Method == http.MethodDelete {
			err := storeInstance.DeleteTarget(r.PathValue("target"))
			if err != nil {

				response.Message = err.Error()
				response.Status = http.StatusBadGateway
				response.Success = false
				json.NewEncoder(w).Encode(response)
				return
			}
			response.Status = http.StatusOK
			response.Success = true
			json.NewEncoder(w).Encode(response)
			return
		}
	}
}
