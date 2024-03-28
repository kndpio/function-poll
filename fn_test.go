package main

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"google.golang.org/protobuf/testing/protocmp"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/crossplane/crossplane-runtime/pkg/logging"
	fnv1beta1 "github.com/crossplane/function-sdk-go/proto/v1beta1"
	"github.com/crossplane/function-sdk-go/resource"
	"github.com/crossplane/function-sdk-go/response"
)

func TestRunFunction(t *testing.T) {

	type args struct {
		ctx context.Context
		req *fnv1beta1.RunFunctionRequest
	}
	type want struct {
		rsp *fnv1beta1.RunFunctionResponse
		err error
	}

	cases := map[string]struct {
		reason string
		args   args
		want   want
	}{
		"Desired resources when poll is in progress": {
			reason: "The Function should return a fatal result if no input was specified",
			args: args{
				req: &fnv1beta1.RunFunctionRequest{
					Input: resource.MustStructJSON(`{
						"slackAPIToken": "SLACKTOKEN",
						"slackNotifyMessage": "Order something?",
						"slackChanelID": "SLACKCHANNEL",
						"providerConfigRef": "",
						"deploymentName": "",
						"deploymentServiceAccount": "",
						"deploymentImage": "",
						"configMap": ""
					}`),
					Observed: &fnv1beta1.State{
						Composite: &fnv1beta1.Resource{
							Resource: resource.MustStructJSON(`
							{
								"apiVersion": "kndp.io/v1alpha1",
								"kind": "Meal",
								"metadata": {
								  "name": "meal",
								  "annotations": {
									"poll.fn.kndp.io/last-sent-time": ""
								  }
								},
								"spec": {
								  "deliveryTime": "",
								  "dueOrderTime": "",
								  "dueTakeTime": "",
								  "employeeRefs": [],
								  "status": ""
								}
							  }

							`),
						},
					},
					Desired: &fnv1beta1.State{
						Resources: map[string]*fnv1beta1.Resource{},
					},
				},
			},
			want: want{
				rsp: &fnv1beta1.RunFunctionResponse{
					Meta: &fnv1beta1.ResponseMeta{Ttl: durationpb.New(response.DefaultTTL)},
					Desired: &fnv1beta1.State{
						Composite: &fnv1beta1.Resource{
							Resource: resource.MustStructJSON(`
							{
								"apiVersion": "kndp.io/v1alpha1",
								"kind": "Meal",
								"metadata": {
								  "annotations": {
									"poll.fn.kndp.io/last-sent-time": ""
								  },
								  "name": "meal"
								},
								"spec": {
								  "deliveryTime": "",
								  "dueOrderTime": "",
								  "dueTakeTime": "",
								  "employeeRefs": [],
								  "status": "done"
								},
								"status": {
								  "conditions": [
									{
									  "lastTransitionTime": null,
									  "reason": "",
									  "status": "True",
									  "type": "Synced"
									}
								  ]
								}
							  }
							`),
						},
					},
				},
			},
		},
		"Desired resources when dueOrderTime is over or all users have voted": {
			reason: "The Function should return Object if dueOrderTime is over or all users have voted",
			args: args{
				req: &fnv1beta1.RunFunctionRequest{
					Input: resource.MustStructJSON(`{
						"slackAPIToken": "SLACKTOKEN",
						"slackNotifyMessage": "Order something?",
						"slackChanelID": "SLACKCHANNEL",
						"providerConfigRef": "",
						"deploymentName": "",
						"deploymentServiceAccount": "",
						"deploymentImage": "",
						"configMap": ""
					}`),
					Observed: &fnv1beta1.State{
						Composite: &fnv1beta1.Resource{
							Resource: resource.MustStructJSON(`
							{
								"apiVersion": "kndp.io/v1alpha1",
								"kind": "Meal",
								"metadata": {
								  "name": "meal",
								  "annotations": {
									"poll.fn.kndp.io/last-sent-time": "100000000000000"
								  }
								},
								"spec": {
								  "deliveryTime": "",
								  "dueOrderTime": "100000000000000",
								  "dueTakeTime": "",
								  "employeeRefs": [],
								  "status": ""
								}
							  }

							`),
						},
					},
					Desired: &fnv1beta1.State{
						Resources: map[string]*fnv1beta1.Resource{},
					},
				},
			},
			want: want{
				rsp: &fnv1beta1.RunFunctionResponse{
					Meta: &fnv1beta1.ResponseMeta{Ttl: durationpb.New(response.DefaultTTL)},
					Desired: &fnv1beta1.State{
						Composite: &fnv1beta1.Resource{
							Resource: resource.MustStructJSON(`
							{
								"apiVersion": "kndp.io/v1alpha1",
								"kind": "Meal",
								"metadata": {
									"annotations": {
										"poll.fn.kndp.io/last-sent-time": "100000000000000"
									},
									"name": "meal"
								},
								"spec": {
									"deliveryTime": "",
									"dueOrderTime": "100000000000000",
									"dueTakeTime": "",
									"employeeRefs": [],
									"status": ""
								},
								"status": {
									"conditions": [
										{
											"lastTransitionTime": null,
											"reason": "",
											"status": "False",
											"type": "Synced"
										}
									]
								}
							}`,
							),
						},
						Resources: map[string]*fnv1beta1.Resource{
							"": {
								Resource: resource.MustStructJSON(`
								{
									"apiVersion": "kubernetes.crossplane.io/v1alpha1",
									"kind": "Object",
									"metadata": {
									  "name": ""
									},
									"spec": {
									  "forProvider": {
										"manifest": {
										  "apiVersion": "apps/v1",
										  "kind": "Deployment",
										  "metadata": {
											"name": "",
											"namespace": "default"
										  },
										  "spec": {
											"replicas": 1,
											"selector": {
											  "matchLabels": {
												"app": "poll"
											  }
											},
											"template": {
											  "metadata": {
												"labels": {
												  "app": "poll"
												}
											  },
											  "spec": {
												"containers": [
												  {
													"envFrom": [
													  {
														"configMapRef": {
														  "name": ""
														}
													  }
													],
													"image": "",
													"name": "poll-container",
													"ports": [
													  {
														"containerPort": 80
													  }
													]
												  }
												],
												"serviceAccountName": ""
											  }
											}
										  }
										}
									  },
									  "managementPolicy": "Default",
									  "providerConfigRef": {
										"name": ""
									  }
									}
								  }
								`),
							},
						},
					},
				},
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := &Function{log: logging.NewNopLogger()}
			rsp, err := f.RunFunction(tc.args.ctx, tc.args.req)

			if diff := cmp.Diff(tc.want.rsp, rsp, protocmp.Transform()); diff != "" {
				t.Errorf("%s\nf.RunFunction(...): -want rsp, +got rsp:\n%s", tc.reason, diff)
			}

			if diff := cmp.Diff(tc.want.err, err, cmpopts.EquateErrors()); diff != "" {
				t.Errorf("%s\nf.RunFunction(...): -want err, +got err:\n%s", tc.reason, diff)
			}
		})
	}
}
