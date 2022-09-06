/*
Copyright © 2022 The dbctl Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package provider

type EngineType string

const (
	MySQLOperator EngineType = "MySQLOperator"
	BitnamiMySQL  EngineType = "BitnamiMySQL"
	WeSQL         EngineType = "WeSQL"
	UnknownEngine EngineType = "UnknownEngine"
)

const (
	helmUser   string = "yimeisun"
	helmPasswd string = "8V+PmX1oSDv4pumDvZp6m7LS8iPgbY3A"
	helmURL    string = "yimeisun.azurecr.io"
)
