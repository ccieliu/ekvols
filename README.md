## EKS Viewer

- A tool to check EKS volumes.

```bash
$ ekvols list
NAMESPACE  PVC            PV                                        CAP  SC   VOLUME_ID              VTYPE  NODE_ID              STATUS  CAP%  IND%  AM   RC      AGE
demo-apps  gp3-ebs-claim  pvc-f8971e41-7eed-4c0e-968b-fcfa5fda0f6e  4Gi  gp3  vol-02da7169dd27ebd93  gp3    i-0ef369bc7a63690dc  Bound   26.0  5.8   RWO  Delete  4d
```