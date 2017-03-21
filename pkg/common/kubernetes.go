package common

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
)

var CA_CERTIFICATE = "/etc/openvswitch/k8s-ca.crt"

func GetPodAnnotations(server, namespace, pod string) (map[string]interface{}, error) {
	//TODO support https
	//caCertificate, apiToken := getApiParams()
	url := server + "/api/v1/namespaces/" + namespace + "/pods/" + pod
	//headers := make(map[string]string)
	//if apiToken {
	//	headers["Authorization"] = "Bearer" + apiToken
	//}
	//if false {
	//response ,err := http.Get(url, headers=headers, verify=ca_certificate)
	//}
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		// TODO: raise here
		return nil, nil
	}
	var podinfo map[string]interface{}
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		fmt.Errorf("fail read data from response: %v", err)
		return nil, err
	}
	err = json.Unmarshal(body, &podinfo)
	if err != nil {
		fmt.Errorf("fail Unmarshal json to podinfo: %v", err)
		return nil, err
	}
	metadata := podinfo["metadata"].(map[string]interface{})
	annotations := metadata["annotations"].(map[string]interface{})
	return annotations, nil

}
